package sqlitedsn

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

type Param struct {
	Key   string
	Value string
}

func P(key, value string) Param {
	return Param{Key: key, Value: value}
}

func File(path string, params ...Param) (string, error) {
	u := url.URL{Scheme: "file"}
	if path == ":memory:" {
		u.Opaque = path
	} else {
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve sqlite path: %w", err)
		}
		uriPath := filepath.ToSlash(absolutePath)
		if runtime.GOOS == "windows" && filepath.VolumeName(absolutePath) != "" && !strings.HasPrefix(uriPath, "/") {
			uriPath = "/" + uriPath
		}
		u.Path = uriPath
	}
	query := url.Values{}
	for _, param := range params {
		query.Add(param.Key, param.Value)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}
