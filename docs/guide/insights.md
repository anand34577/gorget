# Insights and connection history

**Insights** in the console shows how the network is used, for the last 24 hours, 7, 30 or
90 days:

- devices online over time, and traffic through the tunnel (download and upload);
- device status, operating systems and Gorget app versions;
- countries devices connect from (with the country database);
- the devices with the most traffic;
- recent connections: which device, when, for how long, and from which public address.

Every chart has a **Show table** switch with the exact numbers.

The server takes a sample every five minutes and keeps 90 days of samples and 180 days of
connection history. Nothing is collected from inside the tunnel: Gorget never sees which
sites or services people use, only that a device was connected and how many bytes it moved.

## A device's history

**Devices > (a device) > Connections** lists that device's connections with their public
addresses, countries and app versions. If something looks wrong, **Block** in the same
dialog cuts the device off at once; **Unblock** lets it back in with its settings intact.

## Countries

Countries come from a database file on your server; addresses are never sent anywhere.
Under **Settings > Device health > Country database**, choose **Download now** to fetch the
free DB-IP Lite database (about 8 MB), and tick automatic updates to refresh it monthly.
You can use a MaxMind GeoLite2 or GeoIP2 country file instead: put the `.mmdb` file in the
`geoip` folder of the data directory and restart the server.

The same database powers **country rules** (allow or block countries) in device health and
the **new country** email alert.
