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

var viewerPaths = []*regexp.Regexp{
	regexp.MustCompile(`^/me$`),
	regexp.MustCompile(`^/servers$`),
	regexp.MustCompile(`^/servers/[^/]+$`),
	regexp.MustCompile(`^/servers/[^/]+/metrics$`),
	regexp.MustCompile(`^/servers/[^/]+/processes$`),
	regexp.MustCompile(`^/projects$`),
	regexp.MustCompile(`^/projects/[^/]+$`),
	regexp.MustCompile(`^/projects/[^/]+/deployments$`),
	regexp.MustCompile(`^/addons$`),
	regexp.MustCompile(`^/addons/engines$`),
	regexp.MustCompile(`^/services$`),
	regexp.MustCompile(`^/services/engines$`),
	regexp.MustCompile(`^/alerts$`),
	regexp.MustCompile(`^/alerts/count$`),
}

func viewerPathAllowed(p string) bool {
	for _, re := range viewerPaths {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}
