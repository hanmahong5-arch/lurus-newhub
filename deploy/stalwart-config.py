#!/usr/bin/env python3
"""Generate Stalwart config and create k8s ConfigMap."""

import os

# Stalwart's [authentication.fallback-admin] password. Real value lives in
# 重要信息.md / the lurus-newhub K8s Secret. No default — fail loud rather
# than ever bake the shared ops password into a generated config again.
FALLBACK_ADMIN_SECRET = os.environ.get("STALWART_FALLBACK_ADMIN_SECRET")
if not FALLBACK_ADMIN_SECRET:
    raise SystemExit(
        "STALWART_FALLBACK_ADMIN_SECRET is not set. Real value is in "
        "重要信息.md / the lurus-newhub K8s Secret — refusing to generate "
        "a Stalwart config with no fallback-admin secret."
    )

config = """[server]
hostname = "mail.lurus.cn"

[server.listener."http"]
bind = ["[::]:8880"]
protocol = "http"

[server.listener."admin"]
bind = ["[::]:8881"]
protocol = "http"

[server.listener."smtp"]
bind = ["[::]:25"]
protocol = "smtp"

[server.listener."submissions"]
bind = ["[::]:465"]
protocol = "smtp"
tls.implicit = true

[server.listener."submission"]
bind = ["[::]:587"]
protocol = "smtp"

[server.listener."imaptls"]
bind = ["[::]:993"]
protocol = "imap"
tls.implicit = true

[server.listener."imap"]
bind = ["[::]:143"]
protocol = "imap"

[storage]
data = "rocksdb"
fts = "rocksdb"
blob = "rocksdb"
lookup = "rocksdb"
directory = "internal"

[store."rocksdb"]
type = "rocksdb"
path = "/opt/stalwart/data"
compression = "lz4"

[directory."internal"]
type = "internal"
store = "rocksdb"

[tracer."stdout"]
type = "stdout"
level = "info"
ansi = false
enable = true

[authentication.fallback-admin]
user = "admin"
secret = "__FALLBACK_ADMIN_SECRET__"

[server.http]
url = "https://mail.lurus.cn"
use-x-forwarded = true

# SMTP auth: directory, mechanisms, sender matching
[session.auth]
mechanisms = [{if = "local_port != 25 && is_tls", then = "[plain, login]"}, {else = false}]
directory = [{if = "listener != 'smtp'", then = "'internal'"}, {else = false}]
require = [{if = "listener != 'smtp'", then = true}, {else = false}]
must-match-sender = false
allow-plain-text = false
""".replace("__FALLBACK_ADMIN_SECRET__", FALLBACK_ADMIN_SECRET)

with open('/tmp/stalwart-config.toml', 'w') as f:
    f.write(config)

print('Config written to /tmp/stalwart-config.toml')
print('NOTE: certificate.* and signature.* must be set via stalwart-cli (database keys)')
