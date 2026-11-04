//go:build linux

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"phleet/internal/agentproto"
)

// prepareGitAuth turns a deploy plan's credentials into child-process env and
// a cleanup function. Secrets are passed through the environment, never argv,
// and the temporary askpass/key files are removed after clone/fetch.
func (a *Agent) prepareGitAuth(auth agentproto.GitAuth) ([]string, func(), error) {
	noop := func() {}
	kind := strings.ToLower(strings.TrimSpace(auth.Type))
	if kind == "" {
		return nil, noop, nil
	}
	if strings.TrimSpace(auth.Secret) == "" {
		return nil, noop, fmt.Errorf("git auth is configured but the secret is empty")
	}
	tmpRoot := filepath.Join(a.cfg.StateDir, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return nil, noop, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "git-auth-")
	if err != nil {
		return nil, noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	switch kind {
	case "https":
		user := strings.TrimSpace(auth.Username)
		if user == "" {
			user = "git"
		}
		script := filepath.Join(dir, "askpass.sh")
		body := "#!" + a.shPath + "\n" +
			"case \"$1\" in\n" +
			"  *sername*) printf '%s\\n' \"$PHLEET_GIT_USERNAME\" ;;\n" +
			"  *) printf '%s\\n' \"$PHLEET_GIT_PASSWORD\" ;;\n" +
			"esac\n"
		if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
			cleanup()
			return nil, noop, err
		}
		return []string{
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ASKPASS=" + script,
			"GIT_ASKPASS_REQUIRE=force",
			"PHLEET_GIT_USERNAME=" + user,
			"PHLEET_GIT_PASSWORD=" + auth.Secret,
		}, cleanup, nil

	case "ssh":
		key := filepath.Join(dir, "id")
		value := auth.Secret
		if !strings.HasSuffix(value, "\n") {
			value += "\n"
		}
		if err := os.WriteFile(key, []byte(value), 0o600); err != nil {
			cleanup()
			return nil, noop, err
		}
		sshCommand := "ssh -i " + shellQuote(key) +
			" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes"
		return []string{"GIT_SSH_COMMAND=" + sshCommand}, cleanup, nil
	}
	cleanup()
	return nil, noop, fmt.Errorf("unsupported git auth type %q", kind)
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
