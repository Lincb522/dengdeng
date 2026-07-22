package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gorm.io/gorm"
)

const imageStorageConfigID int64 = 1

type ImageStorageView struct {
	Enabled             bool   `json:"enabled"`
	Endpoint            string `json:"endpoint"`
	Region              string `json:"region"`
	Bucket              string `json:"bucket"`
	AccessKeyConfigured bool   `json:"access_key_configured"`
	SecretConfigured    bool   `json:"secret_configured"`
	Prefix              string `json:"prefix"`
	ForcePathStyle      bool   `json:"force_path_style"`
	PublicBaseURL       string `json:"public_base_url"`
	PresignExpiryHours  int    `json:"presign_expiry_hours"`
	MaxDownloadBytes    int64  `json:"max_download_bytes"`
	Active              bool   `json:"active"`
}

type ImageStorageUpdate struct {
	Enabled            bool   `json:"enabled"`
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	AccessKeyID        string `json:"access_key_id"`
	SecretAccessKey    string `json:"secret_access_key"`
	Prefix             string `json:"prefix"`
	ForcePathStyle     bool   `json:"force_path_style"`
	PublicBaseURL      string `json:"public_base_url"`
	PresignExpiryHours int    `json:"presign_expiry_hours"`
	MaxDownloadBytes   int64  `json:"max_download_bytes"`
}

type ImageStorageService struct {
	db         *gorm.DB
	httpClient *http.Client
}

func NewImageStorageService(db *gorm.DB, client *http.Client) *ImageStorageService {
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	return &ImageStorageService{db: db, httpClient: client}
}

func (s *ImageStorageService) Get(ctx context.Context) (model.ImageStorageConfig, error) {
	var cfg model.ImageStorageConfig
	err := s.db.WithContext(ctx).First(&cfg, imageStorageConfigID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cfg = model.ImageStorageConfig{ID: imageStorageConfigID, Region: "auto", Prefix: "images/", PresignExpiryHours: 24, MaxDownloadBytes: 32 << 20}
		return cfg, nil
	}
	return cfg, err
}

func imageStorageView(cfg model.ImageStorageConfig) ImageStorageView {
	configured := strings.TrimSpace(cfg.Bucket) != "" && strings.TrimSpace(string(cfg.AccessKeyID)) != "" && strings.TrimSpace(string(cfg.SecretAccessKey)) != ""
	return ImageStorageView{
		Enabled: cfg.Enabled, Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKeyConfigured: string(cfg.AccessKeyID) != "", SecretConfigured: string(cfg.SecretAccessKey) != "",
		Prefix: cfg.Prefix, ForcePathStyle: cfg.ForcePathStyle, PublicBaseURL: cfg.PublicBaseURL,
		PresignExpiryHours: cfg.PresignExpiryHours, MaxDownloadBytes: cfg.MaxDownloadBytes,
		Active: cfg.Enabled && configured,
	}
}

func (s *ImageStorageService) View(ctx context.Context) (ImageStorageView, error) {
	cfg, err := s.Get(ctx)
	return imageStorageView(cfg), err
}

func (s *ImageStorageService) Update(ctx context.Context, req ImageStorageUpdate) (ImageStorageView, error) {
	cfg, err := s.Get(ctx)
	if err != nil {
		return ImageStorageView{}, err
	}
	req.Endpoint = strings.TrimRight(strings.TrimSpace(req.Endpoint), "/")
	req.Region = strings.TrimSpace(req.Region)
	if req.Region == "" {
		req.Region = "auto"
	}
	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Prefix = strings.Trim(strings.TrimSpace(req.Prefix), "/")
	if req.Prefix != "" {
		req.Prefix += "/"
	}
	req.PublicBaseURL = strings.TrimRight(strings.TrimSpace(req.PublicBaseURL), "/")
	if req.PresignExpiryHours <= 0 || req.PresignExpiryHours > 168 {
		return ImageStorageView{}, fmt.Errorf("presign expiry must be between 1 and 168 hours")
	}
	if req.MaxDownloadBytes < 1<<20 || req.MaxDownloadBytes > 128<<20 {
		return ImageStorageView{}, fmt.Errorf("maximum image size must be between 1 and 128 MiB")
	}
	for _, rawURL := range []string{req.Endpoint, req.PublicBaseURL} {
		if rawURL == "" {
			continue
		}
		parsed, parseErr := url.Parse(rawURL)
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return ImageStorageView{}, fmt.Errorf("image storage URL is invalid")
		}
	}
	if req.Enabled && (req.Bucket == "" || (req.AccessKeyID == "" && string(cfg.AccessKeyID) == "") || (req.SecretAccessKey == "" && string(cfg.SecretAccessKey) == "")) {
		return ImageStorageView{}, fmt.Errorf("bucket and object storage credentials are required when enabled")
	}
	cfg.Enabled, cfg.Endpoint, cfg.Region, cfg.Bucket = req.Enabled, req.Endpoint, req.Region, req.Bucket
	cfg.Prefix, cfg.ForcePathStyle, cfg.PublicBaseURL = req.Prefix, req.ForcePathStyle, req.PublicBaseURL
	cfg.PresignExpiryHours, cfg.MaxDownloadBytes = req.PresignExpiryHours, req.MaxDownloadBytes
	if strings.TrimSpace(req.AccessKeyID) != "" {
		cfg.AccessKeyID = crypto.EncryptedString(strings.TrimSpace(req.AccessKeyID))
	}
	if strings.TrimSpace(req.SecretAccessKey) != "" {
		cfg.SecretAccessKey = crypto.EncryptedString(strings.TrimSpace(req.SecretAccessKey))
	}
	if err := s.db.WithContext(ctx).Save(&cfg).Error; err != nil {
		return ImageStorageView{}, err
	}
	return imageStorageView(cfg), nil
}

func (s *ImageStorageService) s3Client(ctx context.Context, cfg model.ImageStorageConfig) (*s3.Client, error) {
	if strings.TrimSpace(cfg.Bucket) == "" || string(cfg.AccessKeyID) == "" || string(cfg.SecretAccessKey) == "" {
		return nil, fmt.Errorf("object storage is not fully configured")
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(firstImageStorageValue(cfg.Region, "auto")),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(string(cfg.AccessKeyID), string(cfg.SecretAccessKey), "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(loaded, func(options *s3.Options) {
		options.UsePathStyle = cfg.ForcePathStyle
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}), nil
}

func (s *ImageStorageService) Test(ctx context.Context) error {
	cfg, err := s.Get(ctx)
	if err != nil {
		return err
	}
	client, err := s.s3Client(ctx, cfg)
	if err != nil {
		return err
	}
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = client.HeadBucket(testCtx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)})
	return err
}

func (s *ImageStorageService) RewriteImageResult(ctx context.Context, taskID string, raw []byte) ([]byte, error) {
	cfg, err := s.Get(ctx)
	if err != nil || !cfg.Enabled {
		return nil, fmt.Errorf("asynchronous image storage is unavailable")
	}
	client, err := s.s3Client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			B64JSON       string `json:"b64_json,omitempty"`
			URL           string `json:"url,omitempty"`
			RevisedPrompt string `json:"revised_prompt,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Data) == 0 {
		return nil, fmt.Errorf("image response has no data")
	}
	for index := range response.Data {
		data, contentType, fetchErr := s.imageBytes(ctx, response.Data[index].B64JSON, response.Data[index].URL, cfg.MaxDownloadBytes)
		if fetchErr != nil {
			return nil, fetchErr
		}
		key := cfg.Prefix + taskID + fmt.Sprintf("-%d%s", index, imageExtension(contentType))
		_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentType: aws.String(contentType)})
		if err != nil {
			return nil, fmt.Errorf("upload image: %w", err)
		}
		if cfg.PublicBaseURL != "" {
			response.Data[index].URL = cfg.PublicBaseURL + "/" + strings.TrimLeft(key, "/")
		} else {
			expiry := time.Duration(cfg.PresignExpiryHours) * time.Hour
			presigned, signErr := s3.NewPresignClient(client).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key)}, s3.WithPresignExpires(expiry))
			if signErr != nil {
				return nil, signErr
			}
			response.Data[index].URL = presigned.URL
		}
		response.Data[index].B64JSON = ""
	}
	return json.Marshal(response)
}

func (s *ImageStorageService) imageBytes(ctx context.Context, encoded, rawURL string, limit int64) ([]byte, string, error) {
	if strings.TrimSpace(encoded) != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", err
		}
		if int64(len(data)) > limit {
			return nil, "", fmt.Errorf("generated image exceeds storage limit")
		}
		return data, imageContentType(data), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", fmt.Errorf("upstream image URL is invalid")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download image returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, "", fmt.Errorf("downloaded image exceeds storage limit")
	}
	return data, imageContentType(data), nil
}

func imageContentType(data []byte) string {
	contentType := strings.Split(http.DetectContentType(data), ";")[0]
	if !strings.HasPrefix(contentType, "image/") {
		return "image/png"
	}
	return contentType
}

func imageExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func firstImageStorageValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func NewImageTaskID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return "imgtask_" + hex.EncodeToString(raw)
}

func (s *ImageStorageService) CreateTask(ctx context.Context, ownerUserID, ownerKeyID int64) (model.ImageTask, error) {
	if view, err := s.View(ctx); err != nil || !view.Active {
		return model.ImageTask{}, fmt.Errorf("asynchronous image tasks are not enabled")
	}
	// Image tasks are short-lived polling records. Opportunistic cleanup keeps the
	// table bounded even when no separate maintenance worker is running.
	_ = s.CleanupExpired(ctx)
	now := time.Now().UTC()
	task := model.ImageTask{ID: NewImageTaskID(), UserID: ownerUserID, APIKeyID: ownerKeyID, Status: "processing", ExpiresAt: now.Add(24 * time.Hour)}
	return task, s.db.WithContext(ctx).Create(&task).Error
}

func (s *ImageStorageService) GetTask(ctx context.Context, id string, ownerUserID, ownerKeyID int64) (model.ImageTask, error) {
	var task model.ImageTask
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ? AND api_key_id = ? AND expires_at > ?", strings.TrimSpace(id), ownerUserID, ownerKeyID, time.Now()).First(&task).Error
	return task, err
}

func (s *ImageStorageService) FinishTask(ctx context.Context, id, status string, httpStatus int, result, taskError []byte) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&model.ImageTask{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "http_status": httpStatus, "result": string(result), "error": string(taskError), "completed_at": now,
	}).Error
}

func (s *ImageStorageService) CleanupExpired(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", time.Now()).Delete(&model.ImageTask{}).Error
}

func CleanObjectKey(prefix, name string) string {
	return strings.TrimLeft(path.Join("/", prefix, name), "/")
}
