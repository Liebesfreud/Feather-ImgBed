package app

import "time"

type Image struct {
	ID           string         `json:"id"`
	Hash         string         `json:"hash,omitempty"`
	OriginalName string         `json:"original_name"`
	ObjectKey    string         `json:"-"`
	StorageType  string         `json:"storage_type"`
	StorageID    string         `json:"storage_id"`
	MIMEType     string         `json:"mime_type"`
	Size         int64          `json:"size"`
	Width        int            `json:"width,omitempty"`
	Height       int            `json:"height,omitempty"`
	PublicURL    string         `json:"url"`
	ThumbnailURL string         `json:"thumbnail_url,omitempty"`
	DeleteError  string         `json:"delete_error,omitempty"`
	DeletedAt    string         `json:"deleted_at,omitempty"`
	PurgeError   string         `json:"purge_error,omitempty"`
	Favorite     bool           `json:"favorite"`
	CreatedAt    string         `json:"created_at"`
	Variants     []ImageVariant `json:"variants,omitempty"`
}

type ImageVariant struct {
	ID        string `json:"id"`
	ImageID   string `json:"image_id"`
	Kind      string `json:"kind"`
	ObjectKey string `json:"-"`
	PublicURL string `json:"url"`
	MIMEType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	CreatedAt string `json:"created_at"`
}

type StorageRecord struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Enabled   bool           `json:"enabled"`
	Config    map[string]any `json:"config"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type Settings struct {
	SiteName         string             `json:"site_name"`
	SiteURL          string             `json:"site_url"`
	DefaultStorageID string             `json:"default_storage_id"`
	MaxFileSize      int64              `json:"max_file_size"`
	MaxBatchCount    int                `json:"max_batch_count"`
	AllowedTypes     []string           `json:"allowed_types"`
	NamingRule       string             `json:"naming_rule"`
	AllowDuplicates  bool               `json:"allow_duplicates"`
	Processing       ProcessingSettings `json:"processing"`
}

type ProcessingSettings struct {
	GenerateWebP      bool   `json:"generate_webp"`
	WebPQuality       int    `json:"webp_quality"`
	WatermarkEnabled  bool   `json:"watermark_enabled"`
	WatermarkText     string `json:"watermark_text"`
	WatermarkPosition string `json:"watermark_position"`
}

func defaultSettings() Settings {
	return Settings{
		SiteName:        "轻羽图床",
		MaxFileSize:     20 << 20,
		MaxBatchCount:   10,
		AllowedTypes:    []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		NamingRule:      "random",
		AllowDuplicates: false,
		Processing: ProcessingSettings{
			WebPQuality: 82, WatermarkPosition: "bottom-right",
		},
	}
}

type principal struct {
	UserID     int64
	ViaSession bool
	CSRFToken  string
}

type contextKey string

const (
	principalKey contextKey = "principal"
	requestIDKey contextKey = "request_id"
)

func nowUTC() string { return formatTime(time.Now()) }

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
