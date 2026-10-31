package s3

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	minioClient *minio.Client
	bucket      string
}

func NewClient(cfg *config.Config) (*Client, error) {
	client, err := minio.New(cfg.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure: cfg.S3UseSSL,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		minioClient: client,
		bucket:      cfg.S3Bucket,
	}, nil
}

func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.minioClient.BucketExists(ctx, c.bucket)
	if err != nil {
		return err
	}

	if !exists {
		err = c.minioClient.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	_, err := c.minioClient.PutObject(ctx, c.bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

func (c *Client) GetObject(ctx context.Context, key string) (*storage.Object, error) {
	minioObj, err := c.minioClient.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}

	stat, err := minioObj.Stat()
	if err != nil {
		minioObj.Close()
		code := minio.ToErrorResponse(err).Code
		if code == "NoSuchKey" || code == "NoSuchObject" || code == "NotFound" {
			return nil, errors.Join(storage.ErrNotFound, err)
		}
		return nil, err
	}

	return &storage.Object{
		ReadCloser: minioObj,
		Info: storage.ObjectInfo{
			ContentType: stat.ContentType,
			Size:        stat.Size,
			ETag:        strconv.Quote(stat.ETag),
			Modified:    stat.LastModified,
		},
	}, nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	return c.minioClient.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}

func (c *Client) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	_, err := c.minioClient.CopyObject(ctx, minio.CopyDestOptions{
		Bucket: c.bucket,
		Object: dstKey,
	}, minio.CopySrcOptions{
		Bucket: c.bucket,
		Object: srcKey,
	})
	return err
}
