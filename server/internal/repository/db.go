package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// gormLogWriter 将 GORM 日志转发到 logx 统一输出（控制台 + 滚动文件）。
type gormLogWriter struct{}

func (gormLogWriter) Printf(format string, args ...any) {
	logx.PlainInfof(format, args...)
}

// newGormLogger 构建 GORM logger：默认只输出错误与超过阈值的慢查询，
// 避免生产环境刷屏；阈值可通过 PTNEXUS_SLOW_SQL_MS 调节（默认 1000ms）。
func newGormLogger() logger.Interface {
	threshold := time.Second
	if raw := strings.TrimSpace(os.Getenv("PTNEXUS_SLOW_SQL_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			threshold = time.Duration(ms) * time.Millisecond
		}
	}
	return logger.New(
		gormLogWriter{},
		logger.Config{
			SlowThreshold:             threshold,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
}

type Store struct {
	DB     *gorm.DB
	DBType string
}

func NewStore(paths config.RuntimePaths) (*Store, error) {
	dbType := strings.ToLower(strings.TrimSpace(os.Getenv("DB_TYPE")))
	desktopCfg := config.DatabaseConfig{}
	if dbType == "" {
		loadedCfg, found, loadErr := config.LoadDesktopDatabaseConfig(paths)
		if loadErr != nil {
			return nil, loadErr
		}
		if found {
			desktopCfg = loadedCfg
			if loadedCfg.Type != "" {
				dbType = loadedCfg.Type
			}
		}
	}
	if dbType == "" {
		dbType = "sqlite"
	}

	var (
		db  *gorm.DB
		err error
	)

	switch dbType {
	case "mysql":
		host := firstNonEmpty(os.Getenv("MYSQL_HOST"), desktopCfg.MySQL.Host)
		user := firstNonEmpty(os.Getenv("MYSQL_USER"), desktopCfg.MySQL.User)
		password := firstNonEmpty(os.Getenv("MYSQL_PASSWORD"), desktopCfg.MySQL.Password)
		database := firstNonEmpty(os.Getenv("MYSQL_DATABASE"), desktopCfg.MySQL.Database)
		port := firstNonEmpty(os.Getenv("MYSQL_PORT"), intToString(desktopCfg.MySQL.Port), "3306")
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local", user, password, host, port, database)
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: newGormLogger()})
	case "postgresql":
		host := firstNonEmpty(os.Getenv("POSTGRES_HOST"), desktopCfg.PostgreSQL.Host)
		user := firstNonEmpty(os.Getenv("POSTGRES_USER"), desktopCfg.PostgreSQL.User)
		password := firstNonEmpty(os.Getenv("POSTGRES_PASSWORD"), desktopCfg.PostgreSQL.Password)
		database := firstNonEmpty(os.Getenv("POSTGRES_DATABASE"), desktopCfg.PostgreSQL.Database)
		port := firstNonEmpty(os.Getenv("POSTGRES_PORT"), intToString(desktopCfg.PostgreSQL.Port), "5432")
		sslMode := firstNonEmpty(os.Getenv("POSTGRES_SSLMODE"), desktopCfg.PostgreSQL.SSLMode, "disable")
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=Asia/Shanghai", host, user, password, database, port, sslMode)
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: newGormLogger()})
	case "sqlite":
		fallthrough
	default:
		dbType = "sqlite"
		dbPath := filepath.Join(paths.DataDir, "pt_stats.db")
		if value := strings.TrimSpace(os.Getenv("SQLITE_PATH")); value != "" {
			dbPath = value
		} else if value := strings.TrimSpace(desktopCfg.SQLitePath); value != "" {
			dbPath = value
		}
		// WAL 模式显著降低读写锁竞争（读不阻塞写、写不阻塞读），
		// busy_timeout 让写冲突最多等待固定时长而不是立即失败，二者共同避免并发转种时连接被锁占满。
		dsn := dbPath
		if !strings.Contains(dsn, "?") {
			dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
		}
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: newGormLogger()})
	}

	if err != nil {
		return nil, fmt.Errorf("open database failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}
	// 连接池上限按引擎区分：
	//   - SQLite 并发写无益且会放大锁冲突，保持小池；连接设置生命周期上限，
	//     避免长时间占用/僵尸连接累积导致后续请求无限等待连接。
	//   - MySQL/MariaDB/PostgreSQL 走网络、支持并发，池子开太小会让请求在 Go 侧排队等连接，
	//     而 GORM 的慢查询计时把这段等待一并算进 SQL 耗时，日志上就表现成
	//     "十几次索引点查的语句要 6 秒"（实测 Threads_connected=9 已贴着 10 的上限）。
	//     放宽到 50 的依据：服务端 max_connections 默认 151，该库重启以来峰值仅 16，
	//     且 Connection_errors_max_connections=0，余量充足（该库还有其他服务共用）。
	maxOpenConns, maxIdleConns := 10, 5
	if dbType != "sqlite" {
		maxOpenConns, maxIdleConns = 50, 10
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return &Store{DB: db, DBType: dbType}, nil
}

func (s *Store) GroupColumn() string {
	if s.DBType == "postgresql" {
		return `"group"`
	}
	return "`group`"
}

func getEnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func intToString(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}
