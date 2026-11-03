# Database failover runbook

This is the operational procedure for a phleet addon with a standby. Promotion is manual because two
phones have no quorum. The control plane still fences the old primary; epoch numbers make that safe
once the phone reconnects.

The Linux equivalent is `scripts/run-two-agent-pg.sh`; the real-device procedure is
[two-phone-rehearsal.md](two-phone-rehearsal.md). Run one of them before trusting a database with data.

## Before promoting

1. Open **Addons → the addon → Replication**.
2. Confirm the standby is `running` and the lag is acceptable (the table shows milliseconds).
3. Take a backup (**Backup now**) if the primary is reachable. A standby is not a backup.
4. Understand the blast radius: the old primary is stopped, and apps keep their existing connection
   strings. They reconnect through the same relay/loopback facade, now pointed at the new primary.

## Promote

Click **Promote** on the standby row. The control plane:

1. sends `addon.control stop` to the old primary and waits for it (fencing);
2. sends `addon.promote` with `epoch + 1` to the standby (`pg_ctl promote` / `REPLICAOF NO ONE`);
3. repoints the addon's `PinnedServer`, `Port` and `DataDir` at the standby;
4. marks the old placement `demoted` / `stopped` and stops its self-relay;
5. leaves the app relays in place. They resolve the current primary on every new connection, so a
   redeploy is not required.

Verify from the console:

```sql
select pg_is_in_recovery();   -- must be f
create table promote_probe(x int);
insert into promote_probe values (1);
```

For Redis: `SET promote:probe 1` and `GET`.

## If the old primary is offline

The promote still proceeds. When the phone comes back:

- its agent reports the old epoch in its heartbeat;
- the control plane sees `app.epoch < addon.epoch`, sends `addon.control stop`, and marks the
  placement `demoted` locally;
- it can never accept writes again unless you explicitly re-admit it.

Do not manually point apps at the old phone. Its data is stale.

## Re-admit the old phone

1. In **Replication**, click **Remove** on the demoted row. This stops the process, deletes the
   stale data directory, and removes the placement.
2. Click **Add standby** and select that phone. It runs `pg_basebackup` / `REPLICAOF` again from the
   new primary and catches up.

If the phone was factory-reset, the addon's data marker is gone; `Add standby` re-clones it, so this
is the normal path.

## Automatic failover (opt-in)

Automatic failover is available per addon (PostgreSQL and Redis only). Enable **Automatic failover** in the
replication card, set **After (seconds)** and **Max lag (ms)**, and save.

How it works:

1. The primary renews a lease on every heartbeat (`leaseSec`, default 60 s).
2. The agent self-fences at `leaseSec + failoverAfter - 30 s`: if it has not heard from the control plane for that
   long, it stops the engine. This is what prevents the old primary from writing after a promotion.
3. The control plane promotes a running standby once `leaseUntil + failoverAfter` has passed and the standby's last
   measured lag is within `MaxLagMS` (0 ignores the lag check). It fences the old primary first if it is reachable.
4. A promotion advances the epoch. If the old phone comes back with the old epoch, it is stopped and marked demoted.

Defaults: lease 60 s, failover after 120 s, max lag 30 000 ms. The agent lease is 150 s in that configuration; the
control plane promotes at 180 s.

**The trade-off is explicit:** the control plane is the witness. A panel outage longer than the lease becomes a
database outage for addons that opted in, because they must self-fence before another node can take over. Addons with
automatic failover off keep running through a panel outage.

Residual gaps:

- If the **agent process itself is killed** (not just disconnected), the supervised database child can keep running
  until the agent restarts. The epoch fence stops it on reconnect, but there is a window. Prefer killing the phone's
  network/agent cleanly in a rehearsal.
- A **hung engine** still heartbeats (the process is alive), so the lease does not expire. Active health probing would
  be needed for that case; it is not enabled.

If you want the other options:

| Option | What it buys | Cost |
|---|---|---|
| **Manual + epoch fencing** | no split-brain on reconnect; operator decides | downtime until someone clicks Promote |
| **Control-plane lease (implemented, opt-in)** | automatic promotion after a lease expires | the agent stops the engine when it cannot reach the control plane |
| **3 phones + Patroni/etcd** | real quorum, automatic failover | heavy for Termux, 3 devices, more battery |
| **Synchronous replication + pg_rewind** | near-zero data loss; faster rejoin | does not prevent split-brain by itself |

## S3 caveats

- A single S3 request is capped at 96 MB to stay under Cloudflare's 100 MB limit. Clients use
  multipart automatically (`rclone`, `aws s3`, SDKs). For direct/LAN use, start `phleetd` with a
  larger `-storage-max-mb`.
- Bucket mirroring is not automated yet. The practical options are:
  1. a second S3 addon and a scheduled `rclone sync` from the primary bucket, using a relay for the
     remote endpoint;
  2. an off-device backup to Cloudflare R2 / Backblaze B2 with `rclone`, which is what the daily
     backup already gives you on the control plane.
