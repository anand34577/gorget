# Backups and upgrades

## Backups

The server writes an **encrypted** backup of every table daily to `<data_dir>/backups` (7 kept; `backup.interval`, `backup.keep`, `backup.dir`). The file is encrypted with a key derived from your **master key**, so the master key must be backed up separately: without it a backup cannot be read.

```sh
gorget-server backup -out gorget.gbk            # on demand
gorget-server restore -in gorget.gbk            # into an empty database
gorget-server migrate-db -to postgres://…       # copy everything from SQLite to PostgreSQL
gorget-server reset-password -email you@example.com   # lost the owner password
```

Backups restore into SQLite or PostgreSQL alike. With PostgreSQL also use your normal database backups (`pg_dump`, snapshots).

## Upgrades

| How you installed | Upgrade |
|---|---|
| Installer script | Run the same install command again. It keeps your configuration and data, replaces the program and restarts it. |
| Docker | `cd /opt/gorget && docker compose pull && docker compose up -d` (or run `install-server.sh --docker` again). |
| Manual binary | Replace the binary, then `sudo gorget-server restart`. |
| Helm | `helm upgrade gorget ./deploy/helm/gorget --reuse-values` |

Before every upgrade:

1. Back up (`gorget-server backup`; Docker: `docker compose exec gorget gorget-server backup -out /var/lib/gorget/backups/before-upgrade.gbk`).
2. Upgrade. Database migrations run automatically at start; the server refuses to start on a database *newer* than itself, so you cannot accidentally roll back across a migration. To go back, restore the backup into the older version.
3. In a [cluster](../CLUSTER.md) upgrade instances one at a time; devices reconnect to the others meanwhile.

Apps update separately. A client older than the server's minimum protocol version is told to update; newer apps work with older servers for everything that existed in the old version. Re-running the client install command (`curl -fsSL https://vpn.example.com/install.sh | sh`) upgrades a Linux or Mac client.

## Moving to a new server

1. On the old server: `gorget-server backup -out gorget.gbk`, and copy that file **and `master.key`** to the new machine.
2. On the new machine install the program without starting it: `install-server.sh --no-init`, then `sudo gorget-server init` with the same domain.
3. Put `master.key` in the new data folder (or set `GORGET_MASTER_KEY`), then `sudo gorget-server restore -in gorget.gbk`.
4. Point the domain's DNS record at the new machine and run `sudo gorget-server install`. Devices reconnect on their own.
