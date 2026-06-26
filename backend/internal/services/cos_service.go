package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"

	"gps-archive-tool/internal/config"
)

// COSFileInfo COS存储桶中的文件信息
type COSFileInfo struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SizeStr string `json:"size_str"`
	LastMod string `json:"last_mod"`
}

// COSService 腾讯云COS存储桶服务
type COSService struct {
	mu     sync.RWMutex
	client *cos.Client
}

// NewCOSService 创建COS服务
func NewCOSService() *COSService {
	return &COSService{}
}

// ensureClient 确保COS客户端已初始化
func (s *COSService) ensureClient() error {
	s.mu.RLock()
	if s.client != nil {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	cfg := config.Get()
	cc := cfg.COSConfig

	if cc.SecretID == "" || cc.SecretKey == "" || cc.Bucket == "" || cc.Region == "" {
		return fmt.Errorf("COS配置不完整，请检查 secret_id / secret_key / bucket / region")
	}

	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cc.Bucket, cc.Region))
	if err != nil {
		return fmt.Errorf("解析COS地址失败: %w", err)
	}

	s.client = cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  cc.SecretID,
			SecretKey: cc.SecretKey,
		},
		Timeout: 30 * time.Second,
	})

	return nil
}

// ListFiles 列出COS存储桶中的文件
func (s *COSService) ListFiles(prefix string) ([]COSFileInfo, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	cfg := config.Get()
	baseDir := cfg.COSConfig.BaseDir
	if prefix == "" {
		prefix = baseDir
	} else if baseDir != "" {
		prefix = strings.TrimRight(baseDir, "/") + "/" + strings.TrimLeft(prefix, "/")
	}

	var marker string
	var files []COSFileInfo

	for {
		resp, _, err := s.client.Bucket.Get(context.Background(), &cos.BucketGetOptions{
			Prefix:  prefix,
			Marker:  marker,
			MaxKeys: 1000,
		})
		if err != nil {
			return nil, fmt.Errorf("列出COS文件失败: %w", err)
		}

		for _, obj := range resp.Contents {
			key := obj.Key
			// 跳过目录
			if strings.HasSuffix(key, "/") {
				continue
			}
			// 只显示.sql和.gz文件
			ext := strings.ToLower(filepath.Ext(key))
			if ext != ".sql" && ext != ".gz" && ext != ".txt" && ext != ".csv" {
				continue
			}

			name := filepath.Base(key)
			size := int64(obj.Size)
			sizeStr := formatFileSize(size)

			files = append(files, COSFileInfo{
				Key:     key,
				Name:    name,
				Size:    size,
				SizeStr: sizeStr,
				LastMod: obj.LastModified,
			})
		}

		if !resp.IsTruncated {
			break
		}
		marker = resp.NextMarker
	}

	return files, nil
}

// DownloadFile 从COS下载文件到本地
func (s *COSService) DownloadFile(key, localPath string) error {
	return s.DownloadFileWithProgress(key, localPath, nil)
}

// ProgressCallback 下载进度回调(downloadedBytes, totalBytes)
type ProgressCallback func(downloaded, total int64)

// progressReader 包装 io.Reader，每次 Read 后回调进度
type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	callback   ProgressCallback
	lastPct    int // 上次回调时的百分比，避免过于频繁
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.downloaded += int64(n)
	// 每变化至少 2% 才回调用，减少锁竞争
	pct := int(pr.downloaded * 100 / pr.total)
	if pct != pr.lastPct && pr.callback != nil {
		pr.callback(pr.downloaded, pr.total)
		pr.lastPct = pct
	}
	return n, err
}

// DownloadFileWithProgress 从COS下载文件到本地，通过回调报告进度
func (s *COSService) DownloadFileWithProgress(key, localPath string, progressFn ProgressCallback) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	// 确保目录存在
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 先 HEAD 获取文件大小
	resp, err := s.client.Object.Get(context.Background(), key, nil)
	if err != nil {
		return fmt.Errorf("下载COS文件失败: %w", err)
	}
	defer resp.Body.Close()

	contentLength := resp.ContentLength
	if contentLength <= 0 {
		contentLength = 1 // 防除零
	}

	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer out.Close()

	var reader io.Reader = resp.Body
	if progressFn != nil {
		reader = &progressReader{
			reader:   resp.Body,
			total:    contentLength,
			callback: progressFn,
		}
	}

	written, err := io.Copy(out, reader)
	if err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	_ = written

	return nil
}

// formatFileSize 格式化文件大小
func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", float64(size)/(1024*1024*1024))
}
