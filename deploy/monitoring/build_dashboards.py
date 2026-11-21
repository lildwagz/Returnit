#!/usr/bin/env python3
"""Writes the Grafana dashboards in grafana/dashboards/ (provisioned read-only by docker-compose.monitoring.yml).

Edit the panels here, run `python3 build_dashboards.py`, commit the JSON, then recreate Grafana on the server.
Every dashboard carries the tag "returnit", so each one links to the others (dropdown at the top right).
"""
import json
import os

DS = {'type': 'prometheus', 'uid': 'prometheus'}
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'grafana', 'dashboards')
UPDOWN = [{'type': 'value', 'options': {'0': {'text': 'DOWN', 'color': 'red', 'index': 0},
                                        '1': {'text': 'UP', 'color': 'green', 'index': 1}}}]
APP = 'name=~"returnit-prod-.*"'
# Caddy may record a request once per handler in the chain; count only the handlers that answer (or the server level)
CADDY = 'handler=~"reverse_proxy|file_server|static_response|"'


class Board:
    def __init__(self, uid, title, description):
        self.uid, self.title, self.description = uid, title, description
        self.panels, self.y, self.x, self.row_h = [], 0, 0, 0

    def _place(self, w, h):
        if self.x + w > 24:
            self.y, self.x, self.row_h = self.y + self.row_h, 0, 0
        pos = {'x': self.x, 'y': self.y, 'w': w, 'h': h}
        self.x += w
        self.row_h = max(self.row_h, h)
        return pos

    def row(self, title):
        if self.x:
            self.y, self.x, self.row_h = self.y + self.row_h, 0, 0
        self.panels.append({'id': len(self.panels) + 1, 'type': 'row', 'title': title, 'collapsed': False,
                            'gridPos': {'x': 0, 'y': self.y, 'w': 24, 'h': 1}, 'panels': []})
        self.y += 1

    def panel(self, kind, title, exprs, w=12, h=8, unit=None, legend=None, thresholds=None, base='green',
              mappings=None, minv=None, maxv=None, desc=None, decimals=None, stack=False, instant=False):
        exprs = [exprs] if isinstance(exprs, str) else exprs
        legend = [legend] if isinstance(legend, str) else legend
        targets = [{'refId': chr(65 + i), 'datasource': DS, 'expr': e, 'legendFormat': legend[i] if legend else '__auto',
                    **({'instant': True, 'range': False} if instant else {})} for i, e in enumerate(exprs)]
        steps = [{'color': base, 'value': None}] + [{'color': c, 'value': v} for v, c in (thresholds or [])]
        d = {'thresholds': {'mode': 'absolute', 'steps': steps},
             'color': {'mode': 'thresholds' if kind in ('stat', 'gauge', 'bargauge') else 'palette-classic'}}
        for k, v in (('unit', unit), ('min', minv), ('max', maxv), ('decimals', decimals), ('mappings', mappings)):
            if v is not None:
                d[k] = v
        p = {'id': len(self.panels) + 1, 'type': kind, 'title': title, 'datasource': DS, 'targets': targets,
             'gridPos': self._place(w, h), 'fieldConfig': {'defaults': d, 'overrides': []}}
        if desc:
            p['description'] = desc
        if kind == 'stat':
            p['options'] = {'reduceOptions': {'calcs': ['lastNotNull'], 'fields': '', 'values': False},
                            'colorMode': 'background', 'graphMode': 'none' if mappings else 'area',
                            'textMode': 'value_and_name' if legend and '{{' in legend[0] else 'value', 'justifyMode': 'auto',
                            'orientation': 'auto', 'wideLayout': True}
        elif kind == 'bargauge':
            p['options'] = {'reduceOptions': {'calcs': ['lastNotNull'], 'fields': '', 'values': False},
                            'displayMode': 'gradient', 'orientation': 'horizontal', 'showUnfilled': True}
        else:
            p['options'] = {'legend': {'displayMode': 'list', 'placement': 'bottom', 'showLegend': True},
                            'tooltip': {'mode': 'multi', 'sort': 'desc'}}
            d['custom'] = {'lineWidth': 2, 'fillOpacity': 25 if stack else 10, 'showPoints': 'never', 'spanNulls': True,
                           'stacking': {'mode': 'normal' if stack else 'none', 'group': 'A'}}
        self.panels.append(p)

    def stat(self, title, expr, **kw):
        kw.setdefault('w', 4)
        kw.setdefault('h', 4)
        self.panel('stat', title, expr, **kw)

    def graph(self, title, exprs, **kw):
        self.panel('timeseries', title, exprs, **kw)

    def write(self):
        dash = {'uid': self.uid, 'title': self.title, 'description': self.description, 'tags': ['returnit'],
                'timezone': 'browser', 'schemaVersion': 41, 'version': 1, 'refresh': '1m', 'editable': False,
                'graphTooltip': 1, 'time': {'from': 'now-24h', 'to': 'now'}, 'panels': self.panels,
                'templating': {'list': []}, 'annotations': {'list': []},
                'links': [{'type': 'dashboards', 'tags': ['returnit'], 'asDropdown': True, 'title': 'ReturnIt dashboards',
                           'includeVars': False, 'keepTime': True, 'targetBlank': False, 'icon': 'external link'}]}
        os.makedirs(OUT, exist_ok=True)
        with open(os.path.join(OUT, self.uid + '.json'), 'w', encoding='utf-8', newline='\n') as f:
            f.write(json.dumps(dash, indent=2) + '\n')
        print(f'{self.uid}.json: {len(self.panels)} panels')


CPU = '100 * (1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])))'
MEM = '100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)'
DISK = '100 * (1 - node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})'
REQ = f'caddy_http_request_duration_seconds_count{{{CADDY}}}'
ERR5 = f'100 * sum(rate(caddy_http_request_duration_seconds_count{{{CADDY},code=~"5.."}}[5m])) / clamp_min(sum(rate({REQ}[5m])), 1e-9)'
FAILING = 'sum(returnit_object_deletions_failing) + sum(returnit_account_emails_failing) + sum(returnit_identity_deletions_failing)'

# 1. Overview: the page support opens first
b = Board('returnit-overview', 'ReturnIt overview',
          'Is ReturnIt up, is it fast, is background work flowing. Open this first; the other dashboards go deeper.')
b.row('Is it up?')
b.stat('Public services', 'probe_success{job=~"blackbox-(http|redirect|apk)",name!="API (internal)"}', w=12, h=5,
       legend='{{name}}', mappings=UPDOWN, base='red', thresholds=[(1, 'green')])
b.stat('Uptime 24 h (public)', 'avg(avg_over_time(probe_success{job=~"blackbox-(http|redirect|apk)",name!="API (internal)"}[24h])) * 100',
       w=4, h=5, unit='percent', decimals=2, base='red', thresholds=[(99, 'orange'), (99.9, 'green')])
b.stat('Server errors (5xx)', ERR5, w=4, h=5, unit='percent', decimals=2, thresholds=[(1, 'orange'), (5, 'red')],
       desc='Share of requests answered with a 5xx code over the last 5 minutes.')
b.stat('Reminders overdue', 'returnit_notifications_overdue', w=4, h=5, thresholds=[(1, 'red')],
       desc='Reminders due more than 5 minutes ago and still unsent.')
b.row('Health')
b.stat('Failing background jobs', FAILING, thresholds=[(1, 'red')])
b.stat('Tasks given up', 'sum(returnit_queue_tasks{state="archived"})', thresholds=[(1, 'orange')])
b.stat('OOM kills (24 h)', 'increase(node_vmstat_oom_kill[24h])', thresholds=[(1, 'red')], decimals=0,
       desc='Processes the kernel killed for lack of memory, on the whole machine.')
b.stat('Memory used', MEM, unit='percent', thresholds=[(80, 'orange'), (92, 'red')])
b.stat('Disk used', DISK, unit='percent', thresholds=[(80, 'orange'), (90, 'red')])
b.stat('Battery', 'node_power_supply_capacity{power_supply="BAT0"}', unit='percent', base='red',
       thresholds=[(30, 'orange'), (60, 'green')], desc='The laptop battery keeps the server running in a power cut.')
b.row('Speed and traffic')
b.graph('Response time (public, end to end)', 'probe_duration_seconds{job=~"blackbox-(http|redirect|apk)",name!="API (internal)"}',
        unit='s', legend='{{name}}')
b.graph('Requests per second by site', f'sum by (host) (rate({REQ}[5m]))', unit='reqps', legend='{{host}}')
b.graph('Responses by status class', [f'sum(rate({REQ}[5m])) by (code)'], unit='reqps', legend='{{code}}', stack=True)
b.graph('Queue tasks by state', 'sum by (state) (returnit_queue_tasks)', legend='{{state}}')
b.row('Collectors')
b.stat('Scrape targets', 'up', w=24, h=4, legend='{{job}}', mappings=UPDOWN, base='red', thresholds=[(1, 'green')],
       desc='Every source Prometheus reads. DOWN means its numbers are missing from these dashboards.')
b.write()

# 2. Server: the laptop itself
b = Board('returnit-server', 'ReturnIt server', 'The home server: CPU, memory, disk, network, temperature, battery, OOM kills.')
b.row('Now')
b.stat('CPU', CPU, w=3, unit='percent', thresholds=[(70, 'orange'), (90, 'red')])
b.stat('Memory', MEM, w=3, unit='percent', thresholds=[(80, 'orange'), (92, 'red')])
b.stat('Disk /', DISK, w=3, unit='percent', thresholds=[(80, 'orange'), (90, 'red')])
b.stat('Load (1 min)', 'node_load1', w=3, thresholds=[(2, 'orange'), (4, 'red')], desc='2 cores: above 2 the CPU is saturated.')
b.stat('CPU temperature', 'max(node_hwmon_temp_celsius{chip=~".*coretemp.*"})', w=3, unit='celsius', thresholds=[(75, 'orange'), (90, 'red')])
b.stat('Battery', 'node_power_supply_capacity{power_supply="BAT0"}', w=3, unit='percent', base='red', thresholds=[(30, 'orange'), (60, 'green')])
b.stat('On mains power', 'node_power_supply_online{power_supply="AC0"}', w=3, base='red', thresholds=[(1, 'green')],
       mappings=[{'type': 'value', 'options': {'0': {'text': 'ON BATTERY', 'color': 'red', 'index': 0}, '1': {'text': 'YES', 'color': 'green', 'index': 1}}}])
b.stat('Up since', 'time() - node_boot_time_seconds', w=3, unit='dtdurations')
b.row('CPU and memory')
b.graph('CPU use', [CPU, '100 * avg(rate(node_cpu_seconds_total{mode="iowait"}[5m]))'], unit='percent', legend=['used', 'waiting for disk'], maxv=100, minv=0)
b.graph('Memory', ['node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes', 'node_memory_MemTotal_bytes',
                   'node_memory_SwapTotal_bytes - node_memory_SwapFree_bytes'], unit='bytes', legend=['used', 'total', 'swap used'])
b.graph('Load average', ['node_load1', 'node_load5', 'node_load15'], legend=['1 min', '5 min', '15 min'])
b.graph('OOM kills', 'increase(node_vmstat_oom_kill[1h])', legend='per hour', decimals=0,
        desc='The kernel killed a process for lack of memory. See the Containers dashboard for which one.')
b.row('Disk')
b.graph('Disk use /', DISK, unit='percent', legend='/', minv=0, maxv=100)
b.graph('Disk throughput', ['sum(rate(node_disk_read_bytes_total{device=~"sd.*|nvme.*"}[5m]))', 'sum(rate(node_disk_written_bytes_total{device=~"sd.*|nvme.*"}[5m]))'],
        unit='Bps', legend=['read', 'write'])
b.row('Network')
b.graph('Traffic per interface (in)', 'rate(node_network_receive_bytes_total{device=~"wl.*|en.*|tailscale.*"}[5m])', unit='Bps', legend='{{device}}')
b.graph('Traffic per interface (out)', 'rate(node_network_transmit_bytes_total{device=~"wl.*|en.*|tailscale.*"}[5m])', unit='Bps', legend='{{device}}')
b.graph('Network errors and drops', ['sum by (device) (rate(node_network_receive_errs_total{device=~"wl.*|en.*"}[5m]) + rate(node_network_transmit_errs_total{device=~"wl.*|en.*"}[5m]))',
                                     'sum by (device) (rate(node_network_receive_drop_total{device=~"wl.*|en.*"}[5m]) + rate(node_network_transmit_drop_total{device=~"wl.*|en.*"}[5m]))'],
        unit='pps', legend=['errors {{device}}', 'drops {{device}}'])
b.graph('Ping: router and internet', 'probe_icmp_duration_seconds{phase="rtt"}', unit='s', legend='{{name}}',
        desc='A slow or failing router ping points at the Wi-Fi; a slow internet ping with a fast router points at the ISP.')
b.row('Temperature and power')
b.graph('Temperatures', 'node_hwmon_temp_celsius', unit='celsius', legend='{{chip}} {{sensor}}')
b.graph('Battery and mains', ['node_power_supply_capacity{power_supply="BAT0"}', '100 * node_power_supply_online{power_supply="AC0"}'],
        unit='percent', legend=['battery %', 'mains (100 = on)'], minv=0, maxv=100)
b.write()

# 3. Containers
b = Board('returnit-containers', 'ReturnIt containers', 'Every container of the stack: CPU, memory, network, restarts, OOM kills.')
b.row('Problems')
b.stat('OOM kills (24 h)', f'sum(increase(container_oom_events_total{{{APP}}}[24h]))', w=6, thresholds=[(1, 'red')], decimals=0)
b.stat('Restarts (24 h)', f'sum(changes(container_start_time_seconds{{{APP}}}[24h]))', w=6, thresholds=[(1, 'orange'), (5, 'red')], decimals=0)
b.stat('Containers running', f'count(container_last_seen{{{APP}}})', w=6)
b.stat('Memory of all containers', f'sum(container_memory_working_set_bytes{{{APP}}})', w=6, unit='bytes')
b.row('Per container')
b.graph('CPU (1.0 = one full core)', f'sum by (name) (rate(container_cpu_usage_seconds_total{{{APP}}}[5m]))', unit='percentunit', legend='{{name}}')
b.graph('Memory', f'container_memory_working_set_bytes{{{APP}}}', unit='bytes', legend='{{name}}')
b.panel('bargauge', 'Memory against limit (limited containers)', f'100 * container_memory_working_set_bytes{{{APP}}} / (container_spec_memory_limit_bytes{{{APP}}} > 0 < 1e15)',
        w=12, h=8, unit='percent', minv=0, maxv=100, legend='{{name}}', thresholds=[(80, 'orange'), (95, 'red')])
b.graph('OOM kills per container', f'increase(container_oom_events_total{{{APP}}}[1h])', legend='{{name}}', decimals=0)
b.graph('Network in', f'sum by (name) (rate(container_network_receive_bytes_total{{{APP}}}[5m]))', unit='Bps', legend='{{name}}')
b.graph('Network out', f'sum by (name) (rate(container_network_transmit_bytes_total{{{APP}}}[5m]))', unit='Bps', legend='{{name}}')
b.graph('Restarts', f'changes(container_start_time_seconds{{{APP}}}[1h])', legend='{{name}}', decimals=0)
b.graph('Running for', f'time() - container_start_time_seconds{{{APP}}}', unit='s', legend='{{name}}')
b.write()

# 4. Traffic through Caddy
b = Board('returnit-traffic', 'ReturnIt traffic', 'Requests through Caddy per site (api, auth, dl, files, monitor): volume, status codes, latency.')
b.row('Now')
b.stat('Requests per second', f'sum(rate({REQ}[5m]))', w=6, unit='reqps')
b.stat('Server errors (5xx)', ERR5, w=6, unit='percent', decimals=2, thresholds=[(1, 'orange'), (5, 'red')])
b.stat('Client errors (4xx)', f'100 * sum(rate(caddy_http_request_duration_seconds_count{{{CADDY},code=~"4.."}}[5m])) / clamp_min(sum(rate({REQ}[5m])), 1e-9)',
       w=6, unit='percent', decimals=1, thresholds=[(10, 'orange'), (25, 'red')], desc='Many 401/404 can mean a broken app version or someone probing.')
b.stat('In flight', 'sum(caddy_http_requests_in_flight)', w=6)
b.row('Per site')
b.graph('Requests per second', f'sum by (host) (rate({REQ}[5m]))', unit='reqps', legend='{{host}}')
b.graph('Status codes', f'sum by (code) (rate({REQ}[5m]))', unit='reqps', legend='{{code}}', stack=True)
b.graph('Latency p50', f'histogram_quantile(0.5, sum by (le, host) (rate(caddy_http_request_duration_seconds_bucket{{{CADDY}}}[5m])))', unit='s', legend='{{host}}')
b.graph('Latency p95', f'histogram_quantile(0.95, sum by (le, host) (rate(caddy_http_request_duration_seconds_bucket{{{CADDY}}}[5m])))', unit='s', legend='{{host}}')
b.graph('5xx per site', f'sum by (host) (rate(caddy_http_request_duration_seconds_count{{{CADDY},code=~"5.."}}[5m]))', unit='reqps', legend='{{host}}')
b.graph('Bytes sent per site', f'sum by (host) (rate(caddy_http_response_size_bytes_sum{{{CADDY}}}[5m]))', unit='Bps', legend='{{host}}')
b.write()

# 5. Uptime (blackbox)
b = Board('returnit-uptime', 'ReturnIt uptime', 'Probes from the server every minute: public sites through Cloudflare, internal services, router, internet.')
b.row('Status')
b.stat('Every probe', 'probe_success', w=24, h=5, legend='{{name}}', mappings=UPDOWN, base='red', thresholds=[(1, 'green')])
b.row('Uptime')
b.panel('bargauge', 'Uptime 24 h', 'avg_over_time(probe_success[24h]) * 100', w=12, h=9, unit='percent', minv=90, maxv=100,
        decimals=2, legend='{{name}}', base='red', thresholds=[(99, 'orange'), (99.9, 'green')])
b.panel('bargauge', 'Uptime 7 days', 'avg_over_time(probe_success[7d]) * 100', w=12, h=9, unit='percent', minv=90, maxv=100,
        decimals=2, legend='{{name}}', base='red', thresholds=[(99, 'orange'), (99.9, 'green')])
b.row('Timing')
b.graph('Response time', 'probe_duration_seconds{job=~"blackbox-(http|redirect|apk)"}', unit='s', legend='{{name}}')
b.graph('HTTP status returned', 'probe_http_status_code', legend='{{name}}', decimals=0)
b.graph('DNS lookup', 'probe_dns_lookup_time_seconds{job=~"blackbox-(http|redirect|apk)"}', unit='s', legend='{{name}}')
b.graph('Ping', 'probe_icmp_duration_seconds{phase="rtt"}', unit='s', legend='{{name}}')
b.graph('Certificate valid for (Cloudflare edge)', '(probe_ssl_earliest_cert_expiry - time()) / 86400', unit='d', legend='{{name}}', decimals=0)
b.graph('Probe history (1 = up)', 'probe_success', legend='{{name}}', minv=0, maxv=1, decimals=0)
b.write()

# 6. Data stores
b = Board('returnit-data', 'ReturnIt data stores', 'Postgres, Redis and MinIO (receipt files).')
b.row('Postgres')
b.stat('Postgres', 'pg_up', w=4, mappings=UPDOWN, base='red', thresholds=[(1, 'green')])
b.stat('Database size', 'pg_database_size_bytes{datname="returnit"}', w=5, unit='bytes')
b.stat('Connections', 'sum(pg_stat_activity_count{datname="returnit"})', w=5)
b.stat('Max connections', 'pg_settings_max_connections', w=5)
b.stat('Deadlocks (24 h)', 'increase(pg_stat_database_deadlocks{datname="returnit"}[24h])', w=5, thresholds=[(1, 'orange')], decimals=0)
b.graph('Transactions per second', ['rate(pg_stat_database_xact_commit{datname="returnit"}[5m])', 'rate(pg_stat_database_xact_rollback{datname="returnit"}[5m])'],
        unit='ops', legend=['commit', 'rollback'])
b.graph('Connections by state', 'sum by (state) (pg_stat_activity_count{datname="returnit"})', legend='{{state}}', stack=True)
b.graph('Cache hit ratio', 'rate(pg_stat_database_blks_hit{datname="returnit"}[5m]) / clamp_min(rate(pg_stat_database_blks_hit{datname="returnit"}[5m]) + rate(pg_stat_database_blks_read{datname="returnit"}[5m]), 1e-9)',
        unit='percentunit', legend='hit ratio', minv=0, maxv=1)
b.graph('Rows changed per second', ['rate(pg_stat_database_tup_inserted{datname="returnit"}[5m])', 'rate(pg_stat_database_tup_updated{datname="returnit"}[5m])',
                                    'rate(pg_stat_database_tup_deleted{datname="returnit"}[5m])'], unit='ops', legend=['inserted', 'updated', 'deleted'])
b.row('Redis (queues and cache)')
b.stat('Redis', 'redis_up', w=4, mappings=UPDOWN, base='red', thresholds=[(1, 'green')])
b.stat('Memory', 'redis_memory_used_bytes', w=5, unit='bytes')
b.stat('Clients', 'redis_connected_clients', w=5)
b.stat('Keys', 'sum(redis_db_keys)', w=5)
b.stat('Evicted keys (24 h)', 'increase(redis_evicted_keys_total[24h])', w=5, thresholds=[(1, 'orange')], decimals=0)
b.graph('Commands per second', 'rate(redis_commands_processed_total[5m])', unit='ops', legend='commands')
b.graph('Memory', 'redis_memory_used_bytes', unit='bytes', legend='used')
b.row('MinIO (receipt files)')
b.stat('Stored', 'sum(minio_bucket_usage_total_bytes)', w=6, unit='bytes')
b.stat('Files', 'sum(minio_bucket_usage_object_total)', w=6)
b.stat('Free space', 'max(minio_cluster_capacity_usable_free_bytes)', w=6, unit='bytes')
b.stat('S3 errors (24 h)', 'sum(increase(minio_s3_requests_errors_total[24h]))', w=6, thresholds=[(1, 'orange')], decimals=0)
b.graph('S3 requests per second', 'sum by (api) (rate(minio_s3_requests_total[5m]))', unit='reqps', legend='{{api}}')
b.graph('Stored per bucket', 'minio_bucket_usage_total_bytes', unit='bytes', legend='{{bucket}}')
b.write()
