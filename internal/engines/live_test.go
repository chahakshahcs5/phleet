//go:build linux

package engines

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests exercise the rendered commands against real servers. They are
// skipped unless the client/server binaries are on PATH; scripts/portable-pg.sh
// and scripts/portable-redis.sh set that up on a Linux host without root.

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s is not on PATH; install PostgreSQL/Redis clients or use the portable setup", n)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func runScript(t *testing.T, dir, script string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n--- script ---\n%s\n--- output ---\n%s", err, script, out)
	}
	return string(out)
}

func startScript(t *testing.T, dir, script, logPath string) *exec.Cmd {
	t.Helper()
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		f.Close()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return cmd
}

func stopScript(t *testing.T, cmd *exec.Cmd, sig syscall.Signal) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(sig)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func waitPort(t *testing.T, port int, timeout time.Duration, logPath string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	if logPath != "" {
		if b, err := os.ReadFile(logPath); err == nil {
			t.Logf("server log %s:\n%s", logPath, b)
		}
	}
	t.Fatalf("port %s did not open", address)
}

func psqlTry(p Params, sql string) (string, error) {
	cmd := exec.Command("psql", "-h", "127.0.0.1", "-p", strconv.Itoa(p.Port),
		"-U", p.User, "-d", p.Database, "-tAc", sql)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+p.Password)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func psqlQuery(t *testing.T, p Params, sql string) string {
	t.Helper()
	out, err := psqlTry(p, sql)
	if err != nil {
		t.Fatalf("psql %q: %v\n%s", sql, err, out)
	}
	return out
}

func waitPsql(t *testing.T, p Params, sql, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = psqlTry(p, sql)
		if lastErr == nil && last == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("waiting for %q, last value %q (err %v)", want, last, lastErr)
}

func TestPostgresStreamingReplicationLive(t *testing.T) {
	requireTools(t, "initdb", "postgres", "pg_ctl", "pg_basebackup", "pg_isready", "psql")
	base := t.TempDir()
	primary := Params{
		DataDir: filepath.Join(base, "primary"), Port: freePort(t),
		User: "app", Password: "apppass", Database: "appdb",
		Superuser: "phleet_admin", SuperPassword: "adminpass",
		ReplicaUser: "replicator", ReplicaPass: "replpass",
	}
	standby := Params{
		DataDir: filepath.Join(base, "standby"), Port: freePort(t),
		User: "app", Password: "apppass", Database: "appdb",
		Superuser: "phleet_admin", SuperPassword: "adminpass",
		ReplicaUser: "replicator", ReplicaPass: "replpass",
	}

	runScript(t, base, postgresInit(primary))
	primaryLog := filepath.Join(base, "primary.log")
	primaryCmd := startScript(t, primary.DataDir, postgresRun(primary), primaryLog)
	defer stopScript(t, primaryCmd, syscall.SIGINT)
	waitPort(t, primary.Port, 20*time.Second, primaryLog)
	admin := Params{Port: primary.Port, User: primary.Superuser, Password: primary.SuperPassword, Database: "postgres"}
	waitPsql(t, admin, "select 1", "1", 20*time.Second)

	runScript(t, base, postgresPostStart(primary))
	psqlQuery(t, primary, "create table t(x int); insert into t values (1)")

	runScript(t, base, postgresStandbyInit(standby, Primary{
		Host: "127.0.0.1", Port: primary.Port, User: "replicator", Password: "replpass",
	}))
	standbyLog := filepath.Join(base, "standby.log")
	standbyCmd := startScript(t, standby.DataDir, postgresRun(standby), standbyLog)
	defer stopScript(t, standbyCmd, syscall.SIGINT)
	waitPort(t, standby.Port, 20*time.Second, standbyLog)

	waitPsql(t, standby, "select count(*) from t", "1", 25*time.Second)

	lagOut := strings.TrimSpace(runScript(t, base, postgresLag(primary)))
	lag, err := strconv.ParseInt(lagOut, 10, 64)
	if err != nil || lag < 0 {
		t.Fatalf("postgresLag returned %q (err %v)", lagOut, err)
	}
	t.Logf("standby replay lag: %d ms", lag)

	// Fail the primary, then promote the standby exactly as the panel does.
	stopScript(t, primaryCmd, syscall.SIGINT)
	runScript(t, base, postgresPromote(standby))
	runScript(t, base, postgresPromoteReady(standby))
	waitPsql(t, standby, "select pg_is_in_recovery()", "f", 30*time.Second)
	psqlQuery(t, standby, "insert into t values (2)")
	if got := psqlQuery(t, standby, "select count(*) from t"); got != "2" {
		t.Fatalf("promoted standby has %s rows, want 2", got)
	}
}

func redisCLI(t *testing.T, p Params, args ...string) string {
	t.Helper()
	base := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(p.Port),
		"-a", p.Password, "--no-auth-warning"}
	cmd := exec.Command("redis-cli", append(base, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("redis-cli %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func waitRedis(t *testing.T, p Params, key, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = redisCLI(t, p, "GET", key)
		if last == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waiting for %s=%q, last %q", key, want, last)
}

func TestRedisStandbyLive(t *testing.T) {
	requireTools(t, "redis-server", "redis-cli")
	base := t.TempDir()
	primary := Params{
		DataDir: filepath.Join(base, "primary"), Port: freePort(t),
		Password: "primarypass", Database: "0",
	}
	standby := Params{
		DataDir: filepath.Join(base, "standby"), Port: freePort(t),
		Password: "standbypass", Database: "0",
	}

	runScript(t, base, redisInit(primary))
	primaryLog := filepath.Join(base, "primary.log")
	primaryCmd := startScript(t, primary.DataDir, redisRun(primary), primaryLog)
	defer stopScript(t, primaryCmd, syscall.SIGTERM)
	waitPort(t, primary.Port, 15*time.Second, primaryLog)

	runScript(t, base, redisStandbyInit(standby, Primary{
		Host: "127.0.0.1", Port: primary.Port, Password: primary.Password,
	}))
	standbyLog := filepath.Join(base, "standby.log")
	standbyCmd := startScript(t, standby.DataDir, redisRun(standby), standbyLog)
	defer stopScript(t, standbyCmd, syscall.SIGTERM)
	waitPort(t, standby.Port, 15*time.Second, standbyLog)

	redisCLI(t, primary, "SET", "k", "v")
	waitRedis(t, standby, "k", "v", 20*time.Second)

	lagOK := false
	for i := 0; i < 40; i++ {
		out := strings.TrimSpace(runScript(t, base, redisLag(primary)))
		if v, err := strconv.ParseInt(out, 10, 64); err == nil && v >= 0 {
			t.Logf("redis replica lag: %d ms", v)
			lagOK = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !lagOK {
		t.Fatal("redisLag never reported a replica")
	}

	stopScript(t, primaryCmd, syscall.SIGTERM)
	runScript(t, base, redisPromote(standby))
	runScript(t, base, redisPromoteReady(standby))
	redisCLI(t, standby, "SET", "k2", "v2")
	if got := redisCLI(t, standby, "GET", "k2"); got != "v2" {
		t.Fatalf("promoted redis returned %q", got)
	}
}
