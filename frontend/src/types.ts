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
  delete_error?: string
  created_at: string
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
}

export interface ApiToken {
  id: string
  name: string
  last_used_at: string | null
  expires_at: string | null
  created_at: string
}
