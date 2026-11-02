// Package redact removes known credentials while retaining diagnostic context.
package redact

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"whydown/internal/domain"
)

const Mask = "[REDACTED]"

var (
	sensitiveFlag = regexp.MustCompile(`(?i)^--?[a-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|authorization)[a-z0-9_.-]*$`)
	urlPattern    = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s<>"'\x60]+`)
	bearerPattern = regexp.MustCompile(`(?i)\bbearer[ \t]+([a-z0-9._~+/-]+=*)`)
	pairPattern   = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|authorization)[a-z0-9_.-]*)["']?\s*(?:=|:|\s)\s*)(\[REDACTED\]|"[^"\r\n]*"|'[^'\r\n]*'|[a-z][a-z0-9+.-]*://[^\s<>"']+|[^\s&,;"'\}\]]+)`)
	keyPattern    = regexp.MustCompile(`\b(?:gsk_[A-Za-z0-9_-]{12,}|sk-[A-Za-z0-9_-]{12,}|AIza[A-Za-z0-9_-]{20,}|AKIA[A-Z0-9]{16})\b`)
	pemPattern    = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

// Evidence returns an independent sanitized copy. Extra secrets, such as the AI
// credential, are removed even when they occur without a recognizable label.
func Evidence(e domain.Evidence, extraSecrets ...string) domain.Evidence {
	secrets := make(map[string]bool)
	add := func(s string) {
		if s != "" && s != Mask {
			secrets[s] = true
		}
	}
	for _, s := range extraSecrets {
		add(s)
	}
	safeEnv := map[string]bool{"PATH": true, "HOME": true, "HOSTNAME": true, "PORT": true, "NODE_ENV": true, "APP_ENV": true, "ENV": true, "TZ": true, "LANG": true, "LOG_LEVEL": true}
	connectionEnv := map[string]bool{"DATABASE_URL": true, "DB_URL": true, "POSTGRES_URL": true, "MYSQL_URL": true, "REDIS_URL": true, "MONGODB_URI": true, "AMQP_URL": true}
	for _, entry := range e.Env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && !safeEnv[strings.ToUpper(name)] && !(connectionEnv[strings.ToUpper(name)] && urlPattern.MatchString(value)) {
			add(value)
		}
	}
	for i, arg := range e.Command {
		flag, value, inline := strings.Cut(arg, "=")
		if !sensitiveFlag.MatchString(flag) {
			continue
		}
		if inline {
			add(value)
		} else if i+1 < len(e.Command) {
			add(e.Command[i+1])
		}
	}
	// Discover credentials across all fields before replacing any occurrences.
	mapStrings(e, func(s string) string {
		for _, raw := range urlPattern.FindAllString(s, -1) {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			if u.User != nil {
				add(u.User.Username())
				if p, ok := u.User.Password(); ok {
					add(p)
					add(url.QueryEscape(p))
					add(url.PathEscape(p))
				}
			}
		}
		for _, m := range bearerPattern.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
		for _, m := range pairPattern.FindAllStringSubmatch(s, -1) {
			v := strings.Trim(m[2], `"'`)
			if !strings.EqualFold(v, "Bearer") {
				add(v)
			}
		}
		for _, m := range keyPattern.FindAllString(s, -1) {
			add(m)
		}
		return s
	})
	ordered := make([]string, 0, len(secrets))
	for s := range secrets {
		ordered = append(ordered, s)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	replacements := make([]string, 0, len(ordered)*2)
	for _, s := range ordered {
		replacements = append(replacements, s, Mask)
	}
	replace := strings.NewReplacer(replacements...)
	return mapStrings(e, func(s string) string {
		s = pemPattern.ReplaceAllString(s, Mask)
		s = urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
			u, err := url.Parse(raw)
			if err != nil {
				return Mask
			}
			if u.User != nil {
				u.User = url.User(Mask)
			}
			return u.String()
		})
		s = bearerPattern.ReplaceAllString(s, "Bearer "+Mask)
		s = pairPattern.ReplaceAllStringFunc(s, func(pair string) string {
			m := pairPattern.FindStringSubmatch(pair)
			return m[1] + Mask
		})
		s = keyPattern.ReplaceAllString(s, Mask)
		return replace.Replace(s)
	})
}

func mapStrings(e domain.Evidence, f func(string) string) domain.Evidence {
	e.Container.ID = f(e.Container.ID)
	e.Container.Name = f(e.Container.Name)
	e.Container.Image = f(e.Container.Image)
	e.Container.State = f(e.Container.State)
	e.Container.Status = f(e.Container.Status)
	e.Error = f(e.Error)
	e.Health = f(e.Health)
	e.Logs = f(e.Logs)
	for _, target := range []*[]string{&e.Env, &e.Command, &e.HealthLogs, &e.Networks, &e.Warnings} {
		if *target == nil {
			continue
		}
		values := make([]string, len(*target))
		for i, s := range *target {
			values[i] = f(s)
		}
		*target = values
	}
	return e
}
