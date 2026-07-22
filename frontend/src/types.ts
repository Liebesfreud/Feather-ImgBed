export interface ApiEnvelope<T> {
  success: boolean
  data: T
  error?: { code: string; message: string }
  request_id: string
}

export interface ImageItem {
  id: string
  original_name: string
  storage_type: string
  storage_id: string
  mime_type: string
  size: number
  width?: number
  height?: number
  url: string
  thumbnail_url?: string
  delete_error?: string
  deleted_at?: string
  purge_error?: string
  favorite: boolean
  variants?: ImageVariant[]
  created_at: string
}

export interface ImageVariant {
  id: string
  kind: string
  url: string
  mime_type: string
  size: number
  width: number
  height: number
  created_at: string
}

export interface Tag {
  id: string
  name: string
  color: string
  image_count: number
  created_at: string
  updated_at: string
}

export interface Album {
  id: string
  name: string
  description: string
  cover_image_id?: string
  cover_url?: string
  image_count: number
  created_at: string
  updated_at: string
}

export interface StorageRecord {
  id: string
  name: string
  type: 'local' | 's3' | 'webdav' | 'telegram'
  enabled: boolean
  config: Record<string, string | boolean | undefined>
  created_at?: string
  updated_at?: string
}

export interface Settings {
  site_name: string
  site_url: string
  default_storage_id: string
  max_file_size: number
  max_batch_count: number
  allowed_types: string[]
  naming_rule: 'random' | 'date' | 'original'
  allow_duplicates: boolean
  random: RandomSettings
  processing: ProcessingSettings
}

export interface RandomSettings {
  enabled: boolean
  album_id: string
  tag_id: string
}

export interface UploadStatistics {
  image_count: number
  storage_bytes: number
  traffic_bytes: number
}

export interface UploadBootstrap {
  storages: StorageRecord[]
  settings: Settings
  statistics: UploadStatistics
}

export interface ProcessingSettings {
  generate_webp: boolean
  webp_quality: number
  strip_metadata: boolean
  watermark_enabled: boolean
  watermark_text: string
  watermark_position: 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right' | 'center'
}

export interface ApiToken {
  id: string
  name: string
  scopes: TokenScope[]
  last_used_at: string | null
  expires_at: string | null
  created_at: string
}

export type TokenScope = 'images:upload' | 'images:read' | 'images:manage' | 'images:delete' | 'settings:admin'
