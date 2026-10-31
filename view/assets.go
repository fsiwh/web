package view

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"sync"
)

var assetVersion = sync.OnceValue(func() string {
	hash := sha256.New()
	assets := Assets()
	err := fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d:%s:%d:", len(name), name, len(data))
		_, _ = hash.Write(data)
		return nil
	})
	if err != nil {
		panic(fmt.Errorf("fingerprint embedded assets: %w", err))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
})

// AssetVersion identifies the complete embedded bundle, including relative
// module imports and stylesheets' font dependencies.
func AssetVersion() string { return assetVersion() }

func AssetPath(name string) string { return "/assets/" + AssetVersion() + "/" + name }
