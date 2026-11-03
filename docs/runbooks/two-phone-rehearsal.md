# Two-phone PostgreSQL rehearsal

Run this once on real phones before trusting a database with data. It exercises the parts that differ from a
Linux rehearsal: Termux paths, the Android power manager, the phone's PostgreSQL package, the relay between two
devices, and promotion across phones.

The Linux equivalent is `scripts/run-two-agent-pg.sh`, which uses two agents and portable PostgreSQL. Run that in CI
or WSL first; it catches command and orchestration errors without waiting for phones.

## Prerequisites

- Two phones with the agent online (different phones, not two agents on one).
- Enough free space for a copy of the database on both.
- Termux PostgreSQL installed, or let phleet install it on first create (`pkg install postgresql` needs network).
- A control plane reachable by both phones. A LAN URL works; Cloudflare is fine too.
- Disk, battery and thermal headroom. Keep both phones charging for the rehearsal.

## 1. Create the primary on phone A

**Addons → New addon**:

- Name: `Rehearsal DB`
- Engine: PostgreSQL
- Phone: phone A
- Scope: Standalone

Open the addon and wait for `running`. In **Logs** you should see `init:`, `health check passed`.

Run in the **Console**:

```sql
create table t(x int);
insert into t values (1);
select count(*) from t;   -- 1
```

## 2. Add a standby on phone B

In the addon, **Replication → Add a standby on… → phone B**. Watch **Logs**. Expected lines:

```
standby: pg_basebackup: ...
waiting for PostgreSQL to become healthy
```

Wait for the standby row to show `running`. Lag may take up to 30 s to appear (the primary samples it every 30 s).

Verify the standby received the row. From phone B's Termux shell, use the port shown on the standby row and the
credentials from **Connection**:

```bash
psql -h 127.0.0.1 -p <standby-port> -U <user> -d <database> -c 'select count(*) from t;'
```

It must return `1`. A standby accepts read-only queries.

## 3. Promote phone B

Click **Promote** on the standby row and confirm. What happens:

1. The panel sends `addon.control stop` to phone A (fencing).
2. Phone B runs `pg_ctl promote`; the agent waits until `pg_is_in_recovery()` is false.
3. The addon is repointed to phone B; the epoch increments; phone A's placement becomes `demoted`.

Verify:

- Header: phone B, `epoch 2`.
- Console: `select pg_is_in_recovery();` → `f`, then `insert into t values (2); select count(*) from t;` → `2`.
- Phone A's port is closed (`pg_isready` fails or `nc` refuses).
- The replication table shows phone A `demoted` / `stopped`.

## 4. Verify the PC bridge

The addon's **Bridge on this PC** row shows `127.0.0.1:<port>`. From the Windows machine:

```powershell
$env:PGPASSWORD = '<password from Connection>'
psql -h 127.0.0.1 -p <bridge-port> -U <user> -d <database> -c 'select count(*) from t;'
```

It must return `2`. This is the path a desktop client uses; it does not go through Cloudflare.

## 5. Re-admit phone A (optional)

In **Replication**, click **Remove** on phone A. It stops the process, deletes the stale data directory and removes
the placement. Then **Add standby** on phone A; it clones from phone B again.

## 6. Rehearse automatic failover (optional)

1. In **Replication**, enable **Automatic failover**. Set **After** to `30` seconds and **Max lag** to `30000`.
   Keep the lease default (60 s).
2. Note the primary is currently phone B. Open phone A's standby in the table and confirm it is `running`.
3. Simulate a panel outage: stop `phleetd` (or block the Windows machine). Do not stop the phones.
4. The primary (phone B) must **self-fence** after roughly `60 + 30 - 30 = 60` seconds: its PostgreSQL port closes.
   This is the price of automatic failover: a panel outage is a database outage for that addon.
5. Restart `phleetd`. The agents reconnect; once the lease is expired and phone A's standby is healthy, the panel
   promotes phone A automatically. The header shows `epoch 3` and phone A as primary.
6. Verify a write on phone A through the console and that phone B's port stays closed.

If the panel is not restarted, no promotion happens; that is intentional. The control plane is the witness.

## 7. Clean up

**Addons → the addon → Remove database**, check **Also delete the data on the phone**. This stops both placements and
purges both data directories.

## Failure triage

| Symptom | Check |
|---|---|
| Standby stuck at `provisioning` | Logs on both the addon and phone B; `pg_basebackup` usually fails on the replication role, the relay, or disk |
| `password authentication failed for user "replicator"` | The primary did not run `PostStart` with the replication credentials (agent/control plane mismatch); update both and re-provision |
| Promotion 409 / `standby is provisioning` | Wait for the standby to report `running`; lag is not required for a manual promote |
| Promoted primary still read-only | The promotion is not complete; the agent waits for `pg_is_in_recovery() = f` before reporting success |
| Auto-failover did not run | Auto must be enabled, the lease expired (panel was down long enough), the standby running, and lag within the bound; check **Logs** and the addon status |
| Primary did not self-fence | The agent must be running; if the agent process was killed, the database child can outlive it until restart (epoch fencing stops it on reconnect) |
