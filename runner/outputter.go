package runner

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
)

func WritePlainResult(writer io.Writer, verbose bool, includeMetadata bool, result *sources.Result) error {
	var value string
	if includeMetadata {
		value = result.MetadataValue()
	} else {
		value = result.Value()
	}
	if verbose {
		src := result.Source
		if !logger.IsNoColor() {
			src = logger.ColorCyan + result.Source + logger.ColorReset
		}
		_, err := fmt.Fprintf(writer, "[%s] %s\n", src, value)
		return err
	}
	_, err := fmt.Fprintf(writer, "%s\n", value)
	return err
}

type jsonResult struct {
	Source   string            `json:"source"`
	Target   string            `json:"target"`
	Email    string            `json:"email,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Hash     string            `json:"hash,omitempty"`
	Salt     string            `json:"salt,omitempty"`
	IP       string            `json:"ip,omitempty"`
	Phone    string            `json:"phone,omitempty"`
	Name     string            `json:"name,omitempty"`
	Database string            `json:"database,omitempty"`
	URL      string            `json:"url,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

func WriteJSONResult(writer io.Writer, includeMetadata bool, result *sources.Result, target string) error {
	jr := jsonResult{
		Source:   result.Source,
		Target:   target,
		Email:    result.Email,
		Username: result.Username,
		Password: result.Password,
		Hash:     result.Hash,
		Salt:     result.Salt,
		IP:       result.IP,
		Phone:    result.Phone,
		Name:     result.Name,
		URL:      result.URL,
		Extra:    result.Extra,
	}
	if includeMetadata {
		jr.Database = result.Database
	}
	data, err := json.Marshal(jr)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "%s\n", data)
	return err
}
