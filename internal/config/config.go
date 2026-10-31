package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	RegistrationDomains, AccountDomains []string
	Domain                              string
	AppVersion                          string
	SMTPHost                            string
	SMTPPort                            string
	SMTPUser                            string
	SMTPPass                            string
	SMTPFrom                            string
	SMTPReplyTo                         string
	S3Endpoint                          string
	S3Region                            string
	S3Bucket                            string
	S3AccessKey                         string
	S3SecretKey                         string
	S3UseSSL                            bool
}

func New() *Config {
	return &Config{
		RegistrationDomains: domains(os.Getenv("REGISTRATION_DOMAINS")),
		AccountDomains:      domains(os.Getenv("ACCOUNT_DOMAINS")),
		Domain:              getEnv("DOMAIN", "http://localhost:8090"),
		AppVersion:          getEnv("APP_VERSION", ""),
		SMTPHost:            getEnv("SMTP_HOST", ""),
		SMTPPort:            getEnv("SMTP_PORT", "587"),
		SMTPUser:            getEnv("SMTP_USERNAME", ""),
		SMTPPass:            getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:            getEnv("SMTP_FROM", ""),
		SMTPReplyTo:         getEnv("SMTP_REPLY_TO", ""),
		S3Endpoint:          getEnv("S3_ENDPOINT", ""),
		S3Region:            getEnv("S3_REGION", "us-east-1"),
		S3Bucket:            getEnv("S3_BUCKET", ""),
		S3AccessKey:         getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:         getEnv("S3_SECRET_KEY", ""),
		S3UseSSL:            getEnv("S3_USE_SSL", "false") == "true",
	}
}

func domains(value string) []string {
	var result []string
	for _, d := range strings.Split(value, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			result = append(result, d)
		}
	}
	return result
}

func (c *Config) ValidateDomains() error {
	if len(c.AccountDomains) == 0 {
		return fmt.Errorf("ACCOUNT_DOMAINS must list permitted account email domains")
	}
	for _, d := range append(append([]string{}, c.AccountDomains...), c.RegistrationDomains...) {
		if strings.ContainsAny(d, "@/: \\?#") || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
			return fmt.Errorf("invalid account email domain %q", d)
		}
	}
	for _, d := range c.RegistrationDomains {
		found := false
		for _, a := range c.AccountDomains {
			found = found || a == d
		}
		if !found {
			return fmt.Errorf("registration domain %q is missing from ACCOUNT_DOMAINS", d)
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
