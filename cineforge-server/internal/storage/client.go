// Package storage 封装 MinIO/S3 兼容对象存储客户端（对齐 legacy app/services/storage.py）。
// 生产换 OSS 时仅替换此包 + 凭证配置（CINEFORGE_MINIO_* 配置占位不变）。
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"cineforge/server/internal/config"
)

// Client 对象存储客户端。
// 构造不发起网络请求（minio.New 仅校验 endpoint/解析）；bucket 在首次写入时确保存在。
type Client struct {
	bucket string
	mc     *minio.Client
}

// New 构造客户端；凭证未配置（开发占位）返回错误，调用方降级为不可用归档器。
func New(cfg config.MinIOConfig) (*Client, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("minio credentials not configured")
	}
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	return &Client{bucket: cfg.Bucket, mc: mc}, nil
}

// Bucket 默认 bucket（对齐 settings.minio_bucket；files 行各自记录 bucket）。
func (c *Client) Bucket() string { return c.bucket }

// EnsureBucket 对齐 legacy _ensure_bucket：不存在则 make_bucket。
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := c.mc.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", bucket, err)
	}
	if !exists {
		if err := c.mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("make bucket %s: %w", bucket, err)
		}
	}
	return nil
}

// Put 上传对象（part_size 对齐 legacy 10MB；size<0 时自动探测长度）。
func (c *Client) Put(ctx context.Context, bucket, objectKey string, r io.Reader, size int64, contentType string) error {
	if err := c.EnsureBucket(ctx, bucket); err != nil {
		return err
	}
	if _, err := c.mc.PutObject(ctx, bucket, objectKey, r, size, minio.PutObjectOptions{
		ContentType: contentType,
		PartSize:    10 * 1024 * 1024,
	}); err != nil {
		return fmt.Errorf("put object %s/%s: %w", bucket, objectKey, err)
	}
	return nil
}

// Stat 返回对象大小（字节），不存在返回错误。
func (c *Client) Stat(ctx context.Context, bucket, objectKey string) (int64, error) {
	info, err := c.mc.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("stat object %s/%s: %w", bucket, objectKey, err)
	}
	return info.Size, nil
}

// Open 打开对象读流。
//   - length > 0：读 [offset, offset+length)（对齐 legacy open_stored_object 的 offset/length）
//   - length == 0 且 offset == 0：整对象
//   - length == 0 且 offset > 0：从 offset 读到末尾（Range "bytes=N-"）
func (c *Client) Open(ctx context.Context, bucket, objectKey string, offset, length int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	switch {
	case length > 0:
		if err := opts.SetRange(offset, offset+length-1); err != nil {
			return nil, fmt.Errorf("invalid range offset=%d length=%d: %w", offset, length, err)
		}
	case offset > 0:
		if err := opts.SetRange(offset, 0); err != nil {
			return nil, fmt.Errorf("invalid offset %d: %w", offset, err)
		}
	}
	obj, err := c.mc.GetObject(ctx, bucket, objectKey, opts)
	if err != nil {
		return nil, fmt.Errorf("open object %s/%s: %w", bucket, objectKey, err)
	}
	return obj, nil
}

// Remove 删除对象（对齐 legacy delete_stored_object）。
func (c *Client) Remove(ctx context.Context, bucket, objectKey string) error {
	if err := c.mc.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %s/%s: %w", bucket, objectKey, err)
	}
	return nil
}