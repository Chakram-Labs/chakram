package appconfig

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const CurrentVersion = "v2.0.0"

type releaseResponse struct {
	TagName string `json:"tag_name"`
}

func CheckForUpdate() string {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/Chakram-Labs/chakram/releases/latest")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	var rel releaseResponse
	if err := json.Unmarshal(body, &rel); err != nil {
		return ""
	}
	tag := strings.TrimSpace(rel.TagName)
	if tag == "" || tag == CurrentVersion {
		return ""
	}
	return tag
}
