package services

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/tencentyun/cos-go-sdk-v5"

	"gps-archive-tool/internal/config"
)

// 存储提供商常量
const (
	ProviderTencent = "tencent"
	ProviderAliyun  = "aliyun"
)

// COSFileInfo 对象存储桶中的文件信息
type COSFileInfo struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SizeStr string `json:"size_str"`
	LastMod string `json:"last_mod"`
}

// COSService 对象存储服务(支持腾讯云 COS 与阿里云 OSS)
// 通过配置 cos_config.provider 字段决定使用哪个提供商
type COSService struct {
	mu sync.RWMutex
	// 缓存的配置签名(provider+secret_id+bucket+region+endpoint),用于检测配置变更并重建客户端
	configSig string

	tencentClient *cos.Client
	aliyunBucket  *oss.Bucket
}

// NewCOSService 创建对象存储服务
func NewCOSService() *COSService {
	return &COSService{}
}

// configSignature 生成配置签名,用于检测配置变化
func configSignature() string {
	c := config.Get().COSConfig
	return strings.Join([]string{
		strings.ToLower(c.Provider),
		c.SecretID, c.SecretKey, c.Bucket, c.Region, c.Endpoint,
	}, "|")
}

// ensureClient 确保对应提供商的客户端已初始化(配置变化时自动重建)
func (s *COSService) ensureClient() error {
	sig := configSignature()

	s.mu.RLock()
	if s.configSig == sig && (s.tencentClient != nil || s.aliyunBucket != nil) {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 双检
	if s.configSig == sig && (s.tencentClient != nil || s.aliyunBucket != nil) {
		return nil
	}

	// 重置
	s.tencentClient = nil
	s.aliyunBucket = nil

	cfg := config.Get()
	cc := cfg.COSConfig
	provider := strings.ToLower(strings.TrimSpace(cc.Provider))
	if provider == "" {
		provider = ProviderTencent
	}

	if cc.SecretID == "" || cc.SecretKey == "" || cc.Bucket == "" {
		return fmt.Errorf("对象存储配置不完整，请检查 secret_id / secret_key / bucket")
	}

	switch provider {
	case ProviderAliyun, "oss", "ali":
		endpoint := buildAliyunEndpoint(cc.Region, cc.Endpoint)
		if endpoint == "" {
			return fmt.Errorf("阿里云 OSS 配置不完整: region 或 endpoint 至少提供一个")
		}
		client, err := oss.New(endpoint, cc.SecretID, cc.SecretKey,
			oss.Timeout(60, 0), // 连接超时60秒,读超时不限
			oss.EnableCRC(false),
		)
		if err != nil {
			return fmt.Errorf("创建阿里云 OSS 客户端失败: %w", err)
		}
		bucket, err := client.Bucket(cc.Bucket)
		if err != nil {
			return fmt.Errorf("获取阿里云 OSS Bucket 失败: %w", err)
		}
		s.aliyunBucket = bucket
	case ProviderTencent, "cos", "":
		if cc.Region == "" {
			return fmt.Errorf("腾讯云 COS 配置不完整: region 不能为空")
		}
		bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos-internal.%s.myqcloud.com", cc.Bucket, cc.Region))
		if err != nil {
			return fmt.Errorf("解析COS地址失败: %w", err)
		}
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   60 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          10,
		}
		s.tencentClient = cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
			Transport: &cos.AuthorizationTransport{
				SecretID:  cc.SecretID,
				SecretKey: cc.SecretKey,
				Transport: transport,
			},
			Timeout: 0,
		})
	default:
		return fmt.Errorf("不支持的对象存储提供商: %s (仅支持 tencent/aliyun)", cc.Provider)
	}

	s.configSig = sig
	return nil
}

// buildAliyunEndpoint 构造阿里云 OSS endpoint
// 优先使用显式配置的 endpoint,否则基于 region 构造内网地址
func buildAliyunEndpoint(region, endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint != "" {
		// 去掉可能的协议前缀,SDK 会自动补全
		endpoint = strings.TrimPrefix(endpoint, "https://")
		endpoint = strings.TrimPrefix(endpoint, "http://")
		return endpoint
	}
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}
	// region 可能形如 "cn-hangzhou" 或 "oss-cn-hangzhou"
	r := strings.TrimPrefix(region, "oss-")
	// 默认使用内网 endpoint(与腾讯云 cos-internal 行为一致)
	return fmt.Sprintf("oss-%s-internal.aliyuncs.com", r)
}

// Provider 返回当前使用的存储提供商
func (s *COSService) Provider() string {
	cfg := config.Get()
	p := strings.ToLower(strings.TrimSpace(cfg.COSConfig.Provider))
	if p == ProviderAliyun || p == "oss" || p == "ali" {
		return ProviderAliyun
	}
	return ProviderTencent
}

// ListFiles 列出存储桶中的文件(使用配置中的默认 base_dir)
func (s *COSService) ListFiles(prefix string) ([]COSFileInfo, error) {
	cfg := config.Get()
	return s.ListFilesWithBaseDir(prefix, cfg.COSConfig.BaseDir)
}

// ListFilesWithBaseDir 列出存储桶中的文件,可指定自定义 base_dir(控车系统使用独立的目录前缀)
func (s *COSService) ListFilesWithBaseDir(prefix, baseDir string) ([]COSFileInfo, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	if prefix == "" {
		prefix = baseDir
	} else if baseDir != "" {
		prefix = strings.TrimRight(baseDir, "/") + "/" + strings.TrimLeft(prefix, "/")
	}

	s.mu.RLock()
	aliyun := s.aliyunBucket
	tencent := s.tencentClient
	s.mu.RUnlock()

	if aliyun != nil {
		return s.listFilesAliyun(aliyun, prefix)
	}
	if tencent != nil {
		return s.listFilesTencent(tencent, prefix)
	}
	return nil, fmt.Errorf("对象存储客户端未初始化")
}

func (s *COSService) listFilesTencent(client *cos.Client, prefix string) ([]COSFileInfo, error) {
	var marker string
	var files []COSFileInfo
	for {
		resp, _, err := client.Bucket.Get(context.Background(), &cos.BucketGetOptions{
			Prefix:  prefix,
			Marker:  marker,
			MaxKeys: 1000,
		})
		if err != nil {
			return nil, fmt.Errorf("列出COS文件失败: %w", err)
		}
		for _, obj := range resp.Contents {
			if info, ok := buildFileInfo(obj.Key, int64(obj.Size), obj.LastModified); ok {
				files = append(files, info)
			}
		}
		if !resp.IsTruncated {
			break
		}
		marker = resp.NextMarker
	}
	return files, nil
}

func (s *COSService) listFilesAliyun(bucket *oss.Bucket, prefix string) ([]COSFileInfo, error) {
	var marker string
	var files []COSFileInfo
	for {
		resp, err := bucket.ListObjects(
			oss.Prefix(prefix),
			oss.Marker(marker),
			oss.MaxKeys(1000),
		)
		if err != nil {
			return nil, fmt.Errorf("列出OSS文件失败: %w", err)
		}
		for _, obj := range resp.Objects {
			lastMod := obj.LastModified.Format(time.RFC3339)
			if info, ok := buildFileInfo(obj.Key, obj.Size, lastMod); ok {
				files = append(files, info)
			}
		}
		if !resp.IsTruncated {
			break
		}
		marker = resp.NextMarker
		if marker == "" {
			// 兼容部分场景 NextMarker 为空,取最后一个 key 作为 marker
			if n := len(resp.Objects); n > 0 {
				marker = resp.Objects[n-1].Key
			} else {
				break
			}
		}
	}
	return files, nil
}

// buildFileInfo 过滤扩展名并构造文件信息
func buildFileInfo(key string, size int64, lastMod string) (COSFileInfo, bool) {
	if strings.HasSuffix(key, "/") {
		return COSFileInfo{}, false
	}
	ext := strings.ToLower(filepath.Ext(key))
	if ext != ".sql" && ext != ".gz" && ext != ".txt" && ext != ".csv" {
		return COSFileInfo{}, false
	}
	return COSFileInfo{
		Key:     key,
		Name:    filepath.Base(key),
		Size:    size,
		SizeStr: formatFileSize(size),
		LastMod: lastMod,
	}, true
}

// DownloadFile 从对象存储下载文件到本地
func (s *COSService) DownloadFile(key, localPath string) error {
	return s.DownloadFileWithProgress(key, localPath, nil)
}

// ProgressCallback 下载进度回调(downloadedBytes, totalBytes)
type ProgressCallback func(downloaded, total int64)

// progressReader 包装 io.Reader,每次 Read 后回调进度
type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	callback   ProgressCallback
	lastPct    int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.downloaded += int64(n)
	if pr.total > 0 {
		pct := int(pr.downloaded * 100 / pr.total)
		if pct != pr.lastPct && pr.callback != nil {
			pr.callback(pr.downloaded, pr.total)
			pr.lastPct = pct
		}
	}
	return n, err
}

// DownloadFileWithProgress 从对象存储下载文件到本地,通过回调报告进度
func (s *COSService) DownloadFileWithProgress(key, localPath string, progressFn ProgressCallback) error {
	return s.DownloadFileWithProgressCtx(context.Background(), key, localPath, progressFn)
}

// DownloadFileWithProgressCtx 从对象存储下载文件到本地,支持通过 ctx 取消下载
func (s *COSService) DownloadFileWithProgressCtx(ctx context.Context, key, localPath string, progressFn ProgressCallback) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	s.mu.RLock()
	aliyun := s.aliyunBucket
	tencent := s.tencentClient
	s.mu.RUnlock()

	if aliyun != nil {
		return downloadFromAliyun(ctx, aliyun, key, localPath, progressFn)
	}
	if tencent != nil {
		return downloadFromTencent(ctx, tencent, key, localPath, progressFn)
	}
	return fmt.Errorf("对象存储客户端未初始化")
}

func downloadFromTencent(ctx context.Context, client *cos.Client, key, localPath string, progressFn ProgressCallback) error {
	resp, err := client.Object.Get(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("下载COS文件失败: %w", err)
	}
	defer resp.Body.Close()

	contentLength := resp.ContentLength
	if contentLength <= 0 {
		contentLength = 1
	}
	return writeToFile(localPath, resp.Body, contentLength, progressFn)
}

func downloadFromAliyun(ctx context.Context, bucket *oss.Bucket, key, localPath string, progressFn ProgressCallback) error {
	// 先 HEAD 获取文件大小
	meta, err := bucket.GetObjectDetailedMeta(key)
	if err != nil {
		return fmt.Errorf("获取OSS文件元数据失败: %w", err)
	}
	var contentLength int64 = 1
	if cl := meta.Get("Content-Length"); cl != "" {
		var n int64
		if _, err := fmt.Sscanf(cl, "%d", &n); err == nil && n > 0 {
			contentLength = n
		}
	}

	// 支持 ctx 取消:通过管道将 reader 与 ctx 绑定
	body, err := bucket.GetObject(key)
	if err != nil {
		return fmt.Errorf("下载OSS文件失败: %w", err)
	}
	defer body.Close()

	// ctx 取消时关闭 body 以中断读取
	if ctx != nil {
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				body.Close()
			case <-done:
			}
		}()
		defer close(done)
	}

	return writeToFile(localPath, body, contentLength, progressFn)
}

func writeToFile(localPath string, src io.Reader, total int64, progressFn ProgressCallback) error {
	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer out.Close()

	var reader io.Reader = src
	if progressFn != nil {
		reader = &progressReader{
			reader:   src,
			total:    total,
			callback: progressFn,
		}
	}
	if _, err := io.Copy(out, reader); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
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
