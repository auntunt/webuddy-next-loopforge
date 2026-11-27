package gitwork

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// HTTPSRepositoryURL validates a transport URL without identifying the host's
// product. Credentials are requested separately through the Git helper.
func HTTPSRepositoryURL(raw string) (remote, name string, err error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "git clone "))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\\\t\r\n ") {
		return "", "", fmt.Errorf("请提供不含凭据、查询参数或片段的 HTTPS Git 克隆地址")
	}
	host, err := HTTPSHostScope(u.Host)
	if err != nil {
		return "", "", err
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", fmt.Errorf("Git 地址缺少仓库路径")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return "", "", fmt.Errorf("Git 仓库路径无效")
		}
	}
	u.Host, u.Path = host, "/"+path
	if u.RawPath != "" {
		u.RawPath = "/" + strings.Trim(u.RawPath, "/")
	}
	name = strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".git")
	if name == "" {
		return "", "", fmt.Errorf("Git 仓库名称不能为空")
	}
	return u.String(), name, nil
}

// FlexibleGitHTTPURL validates HTTP or HTTPS Git clone URLs including self-hosted
// LAN/IPv4/localhost GitLab/Gitea instances when loose repository onboarding is enabled,
// while still rejecting embedded credentials, query parameters, fragments, and path traversal.
func FlexibleGitHTTPURL(raw string) (remote, name string, err error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "git clone "))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\\\t\r\n ") {
		return "", "", fmt.Errorf("请提供不含凭据、查询参数或片段的 HTTP(S) Git 克隆地址")
	}
	hostname := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if hostname == "" || strings.HasPrefix(hostname, "-") || strings.Contains(hostname, "..") {
		return "", "", fmt.Errorf("Git 仓库主机名无效")
	}
	port := u.Port()
	host := hostname
	if strings.Contains(hostname, ":") && !strings.HasPrefix(hostname, "[") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
			port = ""
		} else {
			host = host + ":" + port
		}
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", fmt.Errorf("Git 地址缺少仓库路径")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return "", "", fmt.Errorf("Git 仓库路径无效")
		}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host, u.Path = host, "/"+path
	if u.RawPath != "" {
		u.RawPath = "/" + strings.Trim(u.RawPath, "/")
	}
	name = strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".git")
	if name == "" {
		return "", "", fmt.Errorf("Git 仓库名称不能为空")
	}
	return u.String(), name, nil
}
