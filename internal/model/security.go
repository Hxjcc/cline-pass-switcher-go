package model

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PublicOrigin accepts an HTTP(S) origin, with an optional trailing root slash.
func PublicOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		(u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || u.RawPath != "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("publicBaseUrl must be a complete http/https origin without credentials, path, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("publicBaseUrl has an invalid port")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

func (c Config) IndependentAdminKey() bool {
	if c.AdminKey == "" || c.AdminKey == c.ProxyKey {
		return false
	}
	for _, key := range c.ProxyKeys {
		if key.Key == c.AdminKey {
			return false
		}
	}
	return true
}

func ValidateConfig(c Config) error {
	if admin := strings.TrimSpace(c.AdminKey); admin != "" {
		for _, key := range c.ProxyKeys {
			if strings.TrimSpace(key.Key) == admin {
				return errors.New("ADMIN_KEY must differ from every issued client key")
			}
		}
	}
	for _, account := range c.Accounts {
		if utf8.RuneCountInString(account.ID) > 256 {
			return errors.New("account id must not exceed 256 characters")
		}
	}
	for _, key := range c.ProxyKeys {
		if utf8.RuneCountInString(key.ID) > 256 {
			return errors.New("proxy key id must not exceed 256 characters")
		}
	}
	_, err := PublicOrigin(c.PublicBaseURL)
	return err
}
