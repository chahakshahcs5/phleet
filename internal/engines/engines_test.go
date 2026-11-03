package engines

import (
	"strings"
	"testing"
)

func TestRegistryIsComplete(t *testing.T) {
	if len(All()) < 3 {
		t.Fatalf("expected at least postgres, redis and sqlite")
	}
	for _, id := range []string{"postgres", "redis", "sqlite", "mariadb", "influxdb"} {
		e, ok := Get(id)
		if !ok {
			t.Fatalf("engine %q missing", id)
		}
		if e.Name == "" || e.Kind == "" || (e.Package == "" && e.Install == nil) || len(e.Binaries) == 0 {
			t.Fatalf("engine %q is incomplete: %+v", id, e)
		}
		if e.Init == nil || e.Run == nil || e.Health == nil || e.Env == nil {
			t.Fatalf("engine %q is missing a command template", id)
		}
		if e.Backup == nil || e.Restore == nil {
			t.Fatalf("engine %q has no backup path", id)
		}
	}
}

func TestReplicationTemplates(t *testing.T) {
	p := Params{
		DataDir: "/home/u/phleet/data/db-app", Port: 4100,
		User: "app", Password: "secret", Database: "app",
		Superuser: "phleet_admin", SuperPassword: "admin-secret",
		ReplicaUser: "replicator", ReplicaPass: "repl-secret",
	}
	pg, _ := Get("postgres")
	init := pg.PostStart(p)
	if !strings.Contains(init, `CREATE ROLE "replicator" REPLICATION LOGIN PASSWORD 'repl-secret'`) {
		t.Fatalf("replication role missing:\n%s", init)
	}
	if !strings.Contains(pg.Init(p), "host replication all 127.0.0.1/32") {
		t.Fatal("pg_hba must allow loopback replication")
	}
	standby := pg.StandbyInit(p, Primary{Host: "127.0.0.1", Port: 16600, User: "replicator", Password: "repl-secret"})
	if !strings.Contains(standby, "pg_basebackup") || !strings.Contains(standby, "-R") {
		t.Fatalf("standby init must use pg_basebackup -R:\n%s", standby)
	}
	if !strings.Contains(pg.Promote(p), "pg_ctl promote") {
		t.Fatal("postgres promote must use pg_ctl promote")
	}
	if pg.PromoteReady == nil || !strings.Contains(pg.PromoteReady(p), "pg_is_in_recovery") {
		t.Fatal("postgres must wait until it is writable after promotion")
	}
	if !strings.Contains(pg.Lag(p), "pg_stat_replication") {
		t.Fatal("postgres lag query is missing")
	}

	redis, _ := Get("redis")
	rstandby := redis.StandbyInit(p, Primary{Host: "127.0.0.1", Port: 16600, Password: "secret"})
	if !strings.Contains(rstandby, "replicaof 127.0.0.1 16600") || !strings.Contains(rstandby, "masterauth") {
		t.Fatalf("redis standby config is wrong:\n%s", rstandby)
	}
	if !strings.Contains(redis.Promote(p), "REPLICAOF NO ONE") {
		t.Fatal("redis promote is wrong")
	}
	if redis.PromoteReady == nil || !strings.Contains(redis.PromoteReady(p), "role:master") {
		t.Fatal("redis must wait until it is a master after promotion")
	}
}

func TestRcloneEngine(t *testing.T) {
	e, ok := Get("rclone")
	if !ok || e.Kind != KindObjectStore || e.Install == nil {
		t.Fatalf("rclone engine is not installable: %+v", e)
	}
	p := Params{DataDir: "/home/u/phleet/data/db-media", Port: 4102, User: "phleet", Password: "hunter2hunter2", Database: "media"}
	if !strings.Contains(e.Install(p), "pkg install -y rclone") || !strings.Contains(e.Install(p), "rclone-current-linux") {
		t.Fatal("rclone install must try the Termux package and a static fallback")
	}
	run := e.Run(p)
	if !strings.Contains(run, "rclone serve s3") || !strings.Contains(run, "127.0.0.1:4102") || !strings.Contains(run, "phleet,hunter2hunter2") {
		t.Fatalf("rclone run command is wrong:\n%s", run)
	}
	env := e.Env(p)
	if env["S3_ENDPOINT"] != "http://127.0.0.1:4102" || env["S3_BUCKET"] != "media" {
		t.Fatalf("rclone env is wrong: %+v", env)
	}
	if !strings.Contains(e.Backup(p, "/tmp/x.tar.gz"), "tar -czf") {
		t.Fatal("rclone backup should archive the bucket directory")
	}
	if BackupExt("rclone") != ".tar.gz" {
		t.Fatal("rclone backups should be tar.gz")
	}
}

func TestServiceEngines(t *testing.T) {
	p := Params{DataDir: "/home/u/phleet/data/db-svc", Port: 4300, User: "phleet", Password: "secret", Database: "svc"}
	for _, id := range []string{"memcached", "mosquitto", "nats", "victoriametrics", "victorialogs", "ntfy", "etcd", "meilisearch", "prometheus", "gitea"} {
		e, ok := Get(id)
		if !ok {
			t.Fatalf("engine %q missing", id)
		}
		if e.Name == "" || e.Kind == "" || len(e.Binaries) == 0 {
			t.Fatalf("engine %q is incomplete: %+v", id, e)
		}
		if e.Init == nil || e.Run == nil || e.Health == nil || e.Env == nil {
			t.Fatalf("engine %q is missing a command template", id)
		}
		run := e.Init(p) + "\n" + e.Run(p)
		if !strings.Contains(run, "127.0.0.1") || !strings.Contains(run, "4300") {
			t.Fatalf("%s must bind loopback on the allocated port:\n%s", id, run)
		}
		if e.NoBackup {
			if e.Backup != nil || e.Restore != nil {
				t.Fatalf("%s has no persistent data and must not expose a backup path", id)
			}
		} else if e.Backup == nil || e.Restore == nil {
			t.Fatalf("%s has no backup path", id)
		}
		if len(e.Env(p)) == 0 {
			t.Fatalf("%s has no project environment", id)
		}
		if e.Install != nil {
			script := e.Install(p)
			if !strings.Contains(script, "uname -m") || !strings.Contains(script, "PHLEET_STATE_DIR") {
				t.Fatalf("%s static install is not architecture-aware:\n%s", id, script)
			}
		}
	}
	// NATS is packaged on Debian but downloaded on Termux.
	nats, _ := Get("nats")
	if got := InstallScriptFor(nats, p, "apt"); !strings.Contains(got, "apt-get install -y") {
		t.Fatalf("nats apt install is wrong: %s", got)
	}
	if got := InstallScriptFor(nats, p, "pkg"); !strings.Contains(got, "nats-server-v2.15.2-linux-arm64.tar.gz") {
		t.Fatalf("nats pkg install must fall back to the static binary: %s", got)
	}
	if BackupExt("nats") != ".tar.gz" || BackupExt("victorialogs") != ".tar.gz" {
		t.Fatal("service backups should be tar.gz")
	}
	// Raw-binary engines download the executable directly, not a tarball.
	meili, _ := Get("meilisearch")
	if !strings.Contains(meili.Install(p), "SRC=archive") {
		t.Fatal("meilisearch should install a raw binary")
	}
	etcd, _ := Get("etcd")
	if got := InstallScriptFor(etcd, p, "apt"); !strings.Contains(got, "etcd-server") {
		t.Fatalf("etcd apt install is wrong: %s", got)
	}
}

func TestVerifyCommands(t *testing.T) {
	p := Params{}
	cases := map[string]string{
		"postgres": "pg_restore --list",
		"redis":    "redis-check-rdb",
		"sqlite":   "quick_check",
		"rclone":   "tar -tzf",
	}
	for engine, want := range cases {
		if got := VerifyCommand(engine, p, "/tmp/dump"); !strings.Contains(got, want) {
			t.Fatalf("%s verify = %q, want %q", engine, got, want)
		}
	}
	if got := VerifyCommand("unknown", p, "/tmp/dump"); !strings.Contains(got, "test -s") {
		t.Fatalf("fallback verify = %q", got)
	}
}

func TestServiceCatalogue(t *testing.T) {
	for _, id := range []string{"filebrowser", "memos", "gotify", "microbin", "navidrome", "syncthing", "vikunja", "miniflux"} {
		e, ok := Get(id)
		if !ok {
			t.Fatalf("service %q missing", id)
		}
		if !e.Service || e.Kind != KindService || e.Category == "" || e.Version == "" || !e.Ingress {
			t.Fatalf("service %q metadata is incomplete: %+v", id, e)
		}
		if e.Install == nil || e.Run == nil {
			t.Fatalf("service %q is missing a lifecycle command", id)
		}
		if e.NoBackup {
			if e.Backup != nil || e.Restore != nil {
				t.Fatalf("service %q has no local data and must not expose a backup path", id)
			}
		} else if e.Backup == nil || e.Restore == nil {
			t.Fatalf("service %q is missing a backup path", id)
		}
		if BackupExt(id) != ".tar.gz" {
			t.Fatalf("service %q backups should be tar.gz", id)
		}
	}
	pg, _ := Get("postgres")
	if pg.Service {
		t.Fatal("postgres must remain an addon, not a service")
	}
	miniflux, _ := Get("miniflux")
	if len(miniflux.Requires) == 0 || miniflux.Requires[0] != "postgres" {
		t.Fatal("miniflux must declare its PostgreSQL dependency")
	}
}

func TestInstallScriptForPackageManagers(t *testing.T) {
	pg, _ := Get("postgres")
	redis, _ := Get("redis")
	p := Params{DataDir: "/home/u/phleet/data/db-app", Port: 4100}

	termux := InstallScriptFor(pg, p, "pkg")
	if !strings.Contains(termux, "pkg install -y") || !strings.Contains(termux, "postgresql") {
		t.Fatalf("termux install is wrong: %s", termux)
	}
	apt := InstallScriptFor(pg, p, "apt")
	if !strings.Contains(apt, "apt-get install -y") || !strings.Contains(apt, "postgresql") {
		t.Fatalf("apt install is wrong: %s", apt)
	}
	if got := InstallScriptFor(redis, p, "apt"); !strings.Contains(got, "redis-server") {
		t.Fatalf("apt must use Debian's package name: %s", got)
	}
	// An unprivileged chroot user needs sudo for apt; the prefix must hit both
	// invocations and nothing else.
	elevated := InstallScriptAs(pg, p, "apt", "sudo -n ")
	if strings.Count(elevated, "sudo -n apt-get") != 2 {
		t.Fatalf("sudo prefix is wrong: %s", elevated)
	}
	// rclone prefers apt on Debian even though it has a custom Termux/static path.
	rclone, _ := Get("rclone")
	if got := InstallScriptFor(rclone, p, "apt"); !strings.Contains(got, "apt-get install -y") || !strings.Contains(got, "rclone") {
		t.Fatalf("apt rclone install is wrong: %s", got)
	}
	// An unknown manager keeps the historical pkg behavior.
	if got := InstallScriptFor(pg, p, ""); !strings.Contains(got, "pkg install -y") {
		t.Fatalf("default install is wrong: %s", got)
	}
}

func TestMariaDBEngine(t *testing.T) {
	e, ok := Get("mariadb")
	if !ok || e.Kind != KindDatabase || e.Package != "mariadb" {
		t.Fatalf("mariadb engine is incomplete: %+v", e)
	}
	p := Params{DataDir: "/home/u/phleet/data/db-app", Port: 4100,
		User: "app", Password: "s3cret", Database: "app", Superuser: "root"}
	if init := e.Init(p); !strings.Contains(init, "mariadb-install-db") || !strings.Contains(init, "--skip-test-db") {
		t.Fatalf("mariadb init is wrong:\n%s", init)
	}
	if run := e.Run(p); !strings.Contains(run, "mariadbd") || !strings.Contains(run, "--bind-address=127.0.0.1") || !strings.Contains(run, "--port=4100") {
		t.Fatalf("mariadb run is wrong:\n%s", run)
	}
	post := e.PostStart(p)
	if !strings.Contains(post, "GRANT ALL PRIVILEGES ON `app`.*") || !strings.Contains(post, "IDENTIFIED BY 's3cret'") {
		t.Fatalf("mariadb setup is wrong:\n%s", post)
	}
	if b := e.Backup(p, "/tmp/x.sql"); !strings.Contains(b, "mariadb-dump") || !strings.Contains(b, "--single-transaction") {
		t.Fatalf("mariadb backup is wrong: %s", b)
	}
	if r := e.Restore(p, "/tmp/x.sql"); !strings.Contains(r, "mariadb --protocol=tcp") || !strings.Contains(r, "< '/tmp/x.sql'") {
		t.Fatalf("mariadb restore is wrong: %s", r)
	}
	if env := e.Env(p); !strings.HasPrefix(env["DATABASE_URL"], "mysql://app:s3cret@127.0.0.1:4100/app") {
		t.Fatalf("mariadb env is wrong: %+v", env)
	}
	if BackupExt("mariadb") != ".sql" {
		t.Fatal("mariadb backups should be .sql")
	}
}

func TestInfluxDBEngine(t *testing.T) {
	e, ok := Get("influxdb")
	if !ok || e.Kind != KindTimeseries || e.Console {
		t.Fatalf("influxdb engine is incomplete: %+v", e)
	}
	p := Params{DataDir: "/home/u/phleet/data/db-metrics", Port: 4101,
		User: "app", Password: "x", Database: "metrics"}
	if run := e.Run(p); !strings.Contains(run, "INFLUXDB_HTTP_BIND_ADDRESS=127.0.0.1:4101") ||
		!strings.Contains(run, "INFLUXDB_META_DIR='/home/u/phleet/data/db-metrics/meta'") {
		t.Fatalf("influx run is wrong:\n%s", run)
	}
	if post := e.PostStart(p); !strings.Contains(post, `CREATE DATABASE "metrics"`) {
		t.Fatalf("influx setup is wrong: %s", post)
	}
	if b := e.Backup(p, "/tmp/x.tar.gz"); !strings.Contains(b, "influxd backup -portable") || !strings.Contains(b, "tar -czf") {
		t.Fatalf("influx backup is wrong: %s", b)
	}
	// Restore must use the same data dirs as the server, or it writes to the
	// distro default (/var/lib/influxdb) and the engine looks empty.
	if r := e.Restore(p, "/tmp/x.tar.gz"); !strings.Contains(r, "INFLUXDB_DATA_DIR='/home/u/phleet/data/db-metrics/data'") {
		t.Fatalf("influx restore is missing the data dir env: %s", r)
	}
	if BackupExt("influxdb") != ".tar.gz" {
		t.Fatal("influxdb backups should be .tar.gz")
	}
}

func TestPostgresCommands(t *testing.T) {
	p := Params{
		DataDir: "/home/u/phleet/data/db-app", Port: 4100,
		User: "app", Password: "secret", Database: "app",
		Superuser: "phleet_admin", SuperPassword: "admin-secret",
	}
	init := postgresInit(p)
	for _, want := range []string{"initdb", "scram-sha-256", "/home/u/phleet/data/db-app"} {
		if !strings.Contains(init, want) {
			t.Fatalf("init script missing %q:\n%s", want, init)
		}
	}
	if strings.Contains(init, "admin-secret") && !strings.Contains(init, "printf") {
		t.Fatalf("password must be written through the pwfile, not as a bare argument")
	}
	run := postgresRun(p)
	if !strings.Contains(run, "listen_addresses=127.0.0.1") || !strings.Contains(run, "-p 4100") {
		t.Fatalf("run command must bind loopback on the allocated port:\n%s", run)
	}
	if !strings.Contains(postgresHealth(p), "pg_isready") {
		t.Fatal("postgres health check should use pg_isready")
	}
	env := postgresEnv(p)
	if !strings.HasPrefix(env["DATABASE_URL"], "postgres://") || !strings.Contains(env["DATABASE_URL"], "127.0.0.1:4100") {
		t.Fatalf("bad database url %q", env["DATABASE_URL"])
	}
}

func TestPostgresEscapesCredentials(t *testing.T) {
	p := Params{Port: 4100, User: "app", Password: "p@ss word/1", Database: "app"}
	got := postgresEnv(p)["DATABASE_URL"]
	if strings.Contains(got, "p@ss word/1") {
		t.Fatalf("password was not URL-escaped: %s", got)
	}
	if !strings.Contains(got, "%40") && !strings.Contains(got, "%20") {
		t.Fatalf("expected percent-encoding in %s", got)
	}
}

func TestRedisCommands(t *testing.T) {
	p := Params{DataDir: "/home/u/phleet/data/db-cache", Port: 4101, Password: "hunter2"}
	init := redisInit(p)
	for _, want := range []string{"requirepass \"hunter2\"", `dir "/home/u/phleet/data/db-cache"`, "appendonly yes"} {
		if !strings.Contains(init, want) {
			t.Fatalf("redis config missing %q:\n%s", want, init)
		}
	}
	if !strings.Contains(redisRun(p), "redis-server") {
		t.Fatal("redis run command is wrong")
	}
	if !strings.Contains(redisHealth(p), "REDISCLI_AUTH=") {
		t.Fatal("redis health check must authenticate without putting the password in ps args")
	}
	if !strings.HasPrefix(redisEnv(p)["REDIS_URL"], "redis://") {
		t.Fatal("bad redis url")
	}
}

func TestSQLiteIsEmbedded(t *testing.T) {
	e, _ := Get("sqlite")
	if !e.Embedded {
		t.Fatal("sqlite must not spawn a server process")
	}
	p := Params{DataDir: "/home/u/phleet/data/db-files", File: "/home/u/phleet/data/db-files/db-files.sqlite"}
	if e.Run(p) != "" {
		t.Fatal("sqlite should have no run command")
	}
	if !strings.Contains(e.Health(p), "quick_check") {
		t.Fatal("sqlite health check should verify the file")
	}
	if BackupExt("sqlite") != ".sqlite" || BackupExt("postgres") != ".dump" || BackupExt("redis") != ".rdb" {
		t.Fatal("unexpected backup extensions")
	}
}
