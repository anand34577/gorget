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

1. Back up (`gorget-server backup`).
2. Replace the binary or image and restart. Database migrations run automatically at start; the server refuses to start on a database *newer* than itself.
3. In a [cluster](../CLUSTER.md) upgrade instances one at a time; devices reconnect to the others meanwhile.

Apps update separately. A client older than the server's minimum protocol version is told to update; newer apps work with older servers for everything that existed in the old version.
