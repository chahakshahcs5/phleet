package httpapi

import "regexp"

// A viewer token is intentionally limited to safe fleet views. Anything that
// returns credentials, source archives, backups, file contents or command
// output requires an admin token.
var viewerWrites = []*regexp.Regexp{
	regexp.MustCompile(`^/logout$`),
	regexp.MustCompile(`^/totp/setup$`),
	regexp.MustCompile(`^/totp/enable$`),
	regexp.MustCompile(`^/totp/disable$`),
}

func viewerWriteAllowed(p string) bool {
	for _, re := range viewerWrites {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// Deployers can drive a project's lifecycle but cannot read credentials,
// source, backups, files or change project configuration.
var deployerWrites = []*regexp.Regexp{
	regexp.MustCompile(`^/projects/[^/]+/deploy$`),
	regexp.MustCompile(`^/projects/[^/]+/restart$`),
	regexp.MustCompile(`^/projects/[^/]+/stop$`),
	regexp.MustCompile(`^/projects/[^/]+/start$`),
	regexp.MustCompile(`^/projects/[^/]+/rollback$`),
}

func deployerWriteAllowed(p string) bool {
	for _, re := range deployerWrites {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

var viewerPaths = []*regexp.Regexp{
	regexp.MustCompile(`^/me$`),
	regexp.MustCompile(`^/servers$`),
	regexp.MustCompile(`^/servers/[^/]+$`),
	regexp.MustCompile(`^/servers/[^/]+/metrics$`),
	regexp.MustCompile(`^/servers/[^/]+/processes$`),
	regexp.MustCompile(`^/projects$`),
	regexp.MustCompile(`^/projects/[^/]+$`),
	regexp.MustCompile(`^/projects/[^/]+/deployments$`),
	regexp.MustCompile(`^/projects/[^/]+/metrics$`),
	regexp.MustCompile(`^/projects/[^/]+/uptime$`),
	regexp.MustCompile(`^/addons$`),
	regexp.MustCompile(`^/addons/engines$`),
	regexp.MustCompile(`^/services$`),
	regexp.MustCompile(`^/services/engines$`),
	regexp.MustCompile(`^/alerts$`),
	regexp.MustCompile(`^/alerts/count$`),
	regexp.MustCompile(`^/tasks$`),
}

func viewerPathAllowed(p string) bool {
	for _, re := range viewerPaths {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}
