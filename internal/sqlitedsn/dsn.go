package sqlitedsn

import (
	"net/url"
)

type Param struct {
	Key   string
	Value string
}

func P(key, value string) Param {
	return Param{Key: key, Value: value}
}

func File(path string, params ...Param) string {
	u := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	for _, param := range params {
		query.Add(param.Key, param.Value)
	}
	u.RawQuery = query.Encode()
	return u.String()
}
