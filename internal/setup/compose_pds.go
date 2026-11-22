package setup

import (
	"strings"

	"github.com/vidra/vidra-core/internal/config"
)

func localPDSSelected(values map[string]string) bool {
	return IsTrue(values["ATPROTO_ENABLED"]) && strings.TrimSpace(values["ATPROTO_PDS_URL"]) == ""
}

// ComposeInvocation returns the executable and separate arguments for a
// read-only diagnostic. Like deploy/lib.sh, supply the derived hostname only
// when the local PDS overlay is active. The diagnostic never creates keys.
func ComposeInvocation(envPath string, values map[string]string, tail ...string) []string {
	args := []string{"docker"}
	if localPDSSelected(values) {
		cfg := config.Config{ATProtoEnabled: true, PublicBaseURL: values["PUBLIC_BASE_URL"]}
		host := ""
		if localPDSSite(cfg.PublicBaseURL) {
			host = strings.TrimPrefix(cfg.DefaultATProtoPDS(), "https://")
		}
		args = []string{"env", "VIDRA_LOCAL_PDS_HOST=" + host, "docker"}
	}
	return append(args, RenderCheckArgs(envPath, values, tail...)...)
}

// QuoteInvocation formats the argv for an operator copying a diagnostic.
func QuoteInvocation(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
