package service

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// HeaderLink is stored in the existing settings table as part of a JSON array.
type HeaderLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func NormalizeHeaderLinks(items []HeaderLink) ([]HeaderLink, error) {
	if len(items) > 2 {
		return nil, errors.New("at most two header links are allowed")
	}
	result := make([]HeaderLink, 0, len(items))
	for _, item := range items {
		item.Title = strings.TrimSpace(item.Title)
		item.URL = strings.TrimSpace(item.URL)
		if utf8.RuneCountInString(item.Title) > 50 {
			return nil, errors.New("header link title is too long (max 50 characters)")
		}
		if len(item.URL) > 2048 {
			return nil, errors.New("header link URL is too long (max 2048 characters)")
		}
		if item.URL != "" {
			// Navigation destinations may include query parameters and fragments.
			parsed, err := url.Parse(item.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
				return nil, errors.New("header link URL must be an absolute http(s) URL")
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func ParseHeaderLinks(raw string) []HeaderLink {
	var items []HeaderLink
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []HeaderLink{}
	}
	items, err := NormalizeHeaderLinks(items)
	if err != nil {
		return []HeaderLink{}
	}
	return items
}

// PublicHeaderLinks omits incomplete entries while preserving configured order.
func PublicHeaderLinks(raw string) []HeaderLink {
	result := make([]HeaderLink, 0, 2)
	for _, item := range ParseHeaderLinks(raw) {
		if item.Title != "" && item.URL != "" {
			result = append(result, item)
		}
	}
	return result
}
