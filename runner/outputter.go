package runner

import (
	"encoding/json"
	"fmt"
	"github.com/vflame6/leaker/runner/sources"
	"io"
)

func WritePlainResult(writer io.Writer, verbose bool, result *sources.Result) error {
	value := result.Value()
	if verbose {
		_, err := fmt.Fprintf(writer, "[%s] %s\n", result.Source, value)
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
	IP       string            `json:"ip,omitempty"`
	Phone    string            `json:"phone,omitempty"`
	Name     string            `json:"name,omitempty"`
	Database string            `json:"database,omitempty"`
	URL      string            `json:"url,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

func WriteJSONResult(writer io.Writer, result *sources.Result, target string) error {
	data, err := json.Marshal(jsonResult{
		Source:   result.Source,
		Target:   target,
		Email:    result.Email,
		Username: result.Username,
		Password: result.Password,
		Hash:     result.Hash,
		IP:       result.IP,
		Phone:    result.Phone,
		Name:     result.Name,
		Database: result.Database,
		URL:      result.URL,
		Extra:    result.Extra,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "%s\n", data)
	return err
}
