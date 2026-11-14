package content

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// v148Supplement holds the v148 client data the v145 data root lacks, laid out
// like the root itself (v148/Map.wz/Map/Map9/915000000.img.xml). The files are
// written by cmd/wzexport; see v148/README.md for the list.
//
//go:embed all:v148
var v148Supplement embed.FS

// openWZ opens a file of the data root. A file the root does not have is
// served from the embedded v148 supplement when it is listed there.
func openWZ(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return f, err
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if strings.HasSuffix(parts[i], ".wz") {
			if extra, e := v148Supplement.Open("v148/" + strings.Join(parts[i:], "/")); e == nil {
				return extra, nil
			}
			break
		}
	}
	return nil, err
}
