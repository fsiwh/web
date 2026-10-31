// Package disk stores objects beneath a filesystem root using the same keys as S3.
package disk

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"github.com/fachschaftinformatik/web/storage"
)

type Store struct{ root *os.Root }

func New(path string) (*Store, error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Close() error                           { return s.root.Close() }
func (s *Store) EnsureBucket(ctx context.Context) error { return ctx.Err() }

func (s *Store) GetObject(ctx context.Context, key string) (*storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := s.root.Open(key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(storage.ErrNotFound, err)
		}
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("object is not a regular file")
	}
	contentType := mime.TypeByExtension(filepath.Ext(key))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &storage.Object{ReadCloser: file, Info: storage.ObjectInfo{
		ContentType: contentType,
		Size:        info.Size(),
		ETag:        fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
		Modified:    info.ModTime(),
	}}, nil
}

type reader struct {
	ctx context.Context
	io.Reader
}

func (r reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func (s *Store) Upload(ctx context.Context, key string, input io.Reader, size int64, contentType string) error {
	if size < 0 {
		return errors.New("invalid object size")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(key)
	if err := s.root.MkdirAll(directory, 0700); err != nil {
		return err
	}
	temporary := filepath.Join(directory, ".upload"+rand.Text())
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(temporary)
	written, err := io.Copy(file, reader{ctx, io.LimitReader(input, size+1)})
	if err != nil {
		file.Close()
		return err
	}
	if written != size {
		file.Close()
		return errors.New("object size does not match input")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.root.Rename(temporary, key)
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) CopyObject(ctx context.Context, source, destination string) error {
	object, err := s.GetObject(ctx, source)
	if err != nil {
		return err
	}
	defer object.Close()
	return s.Upload(ctx, destination, object, object.Info.Size, object.Info.ContentType)
}
