export interface SiteData {
  uploaded: number
  comment: string
  migration: number
  state: string
  seeders: number
  torrentId?: string
  site?: string
  site_name?: string
  // 该站点副本的抓取参数是否"维护通过"（可发布）——仅一种多站
  // only_publishable 查询返回；转种源站候选据此限定。
  reviewed?: boolean
}

export interface Torrent {
  unique_id: string
  name: string
  save_path: string
  size: number
  size_formatted: string
  progress: number
  state: string
  sites: Record<string, SiteData>
  total_uploaded: number
  total_uploaded_formatted: string
  seeders: number
  downloaderId?: string
  downloaderIds?: string[]
  target_sites_count?: number
}

export interface ISourceInfo {
  name: string
  site: string
  torrentId: string
}

export interface Downloader {
  id: string
  name: string
  enabled: boolean
  color?: string
}
