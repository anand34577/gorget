# Device health

Require devices to be in good shape before they join, or just see who is not.

**Settings → Device health**

| Rule | Checked by the app |
|---|---|
| Oldest Gorget app version | `0.3.0` style version numbers |
| Oldest operating system | per OS: Windows `10.0.19045`, macOS `13`, Linux kernel/distro version, Android `10` |
| Disk encryption | BitLocker (needs admin rights, which the service has), FileVault, LUKS; Android reports it as on |
| System firewall | Windows Defender Firewall (all profiles), macOS application firewall, ufw / firewalld / nftables |
| Allowed operating systems | for example only Linux and Android |
| Where it connects from | public address ranges, such as your office |
| Countries | allow only some countries, or block some; looked up by the server from the address the device connects from (see [Insights](insights.md#countries) for the country database) |
| Exempt tags | e.g. `tag:server`, for machines that cannot report |

**Mode:** *Block* keeps a non-compliant device off the network (it is told exactly what to fix, shown in the app, and joins by itself within a minute of fixing it) or *Report only* flags it in the console first, a good way to see who would be affected before you enforce.

## In the console

Devices that break a rule show a **Health** badge (hover for the reasons), a *Health issues* filter, and a dot on the overview. The overview lists how many devices need attention.

## Limits

The app reports what it can detect; a modified client can report anything. Treat these rules as hygiene that keeps honest devices healthy, not as proof against a hostile device. Old clients that do not report a value fail a *required* rule (the reason says "unknown").

Country and address rules are checked by the **server** against the address it sees, so a client can't fake them. They do see the address of whatever the device uses to reach the internet: a device behind another VPN or a proxy appears where that service is. With an allow list, a device whose country can't be determined is blocked.
