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
# cAdvisor sees containers as cgroups (Docker 29); container-info maps the cgroup id to the name.
CG = 'id=~"/system.slice/docker-.*"'
NAMES = f'returnit_container_info{{{APP}}}'


def named(expr):
    """Adds the container name to a cAdvisor series (joined on the cgroup id)."""
    return f'({expr} * on (id) group_left (name) {NAMES})'


# Caddy records a request once per handler it passes. Every site runs `headers` (import secure) except the bare-domain
# redirect, which only has static_response, so counting those two counts each request once. Each port is a named
# server (Caddyfile.tunnel): api, auth, dl, files, apex, monitor.
CADDY = 'handler=~"headers|static_response",server!~"srv[0-9]+"'


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
              mappings=None, minv=None, maxv=None, desc=None, decimals=None, stack=False, instant=False,
              legend_table=None, overrides=None, bars=None):
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
             'gridPos': self._place(w, h), 'fieldConfig': {'defaults': d, 'overrides': overrides or []}}
        if desc:
            p['description'] = desc
        if kind == 'stat':
            p['options'] = {'reduceOptions': {'calcs': ['lastNotNull'], 'fields': '', 'values': False},
                            'colorMode': 'background', 'graphMode': 'none' if mappings else 'area',
                            'textMode': 'value_and_name' if legend and '{{' in legend[0] else 'value', 'justifyMode': 'auto',
                            'orientation': 'auto', 'wideLayout': True}
        elif kind == 'bargauge':
            p['options'] = {'reduceOptions': {'calcs': ['lastNotNull'], 'fields': '', 'values': False},
                            'displayMode': bars or 'gradient', 'orientation': 'horizontal', 'showUnfilled': True,
                            'valueMode': 'color', 'namePlacement': 'left', 'sizing': 'auto', 'minVizHeight': 16}
        else:
            legend_opts = {'displayMode': 'list', 'placement': 'bottom', 'showLegend': True}
            if legend_table:  # e.g. ['mean', 'max', 'lastNotNull'], sorted by the second one
                legend_opts = {'displayMode': 'table', 'placement': 'right', 'showLegend': True, 'calcs': legend_table,
                               'sortBy': {'mean': 'Mean', 'max': 'Max', 'lastNotNull': 'Last *'}[legend_table[1]], 'sortDesc': True}
            p['options'] = {'legend': legend_opts, 'tooltip': {'mode': 'multi', 'sort': 'desc'}}
            d['custom'] = {'lineWidth': 2, 'fillOpacity': 25 if stack else 10, 'showPoints': 'never', 'spanNulls': True,
                           'stacking': {'mode': 'normal' if stack else 'none', 'group': 'A'}}
        self.panels.append(p)

    def probe_table(self, title, sel, h, desc=None, cert=True):
        """One row per probe: status, history (bars, red when down), response time, 24 h uptime, certificate days."""
        by = 'max by (name)'
        q = [  # refId, expr, range query?
            ('A', f'{by} (probe_success{{{sel}}}) + 0.5', True),          # history: 1.5 when up, 0.5 when down
            ('B', f'{by} (probe_duration_seconds{{{sel}}})', True),        # response time
            ('C', f'{by} (avg_over_time(probe_success{{{sel}}}[24h])) * 100', False),
            ('E', f'{by} (probe_success{{{sel}}})', False),                # status now
        ]
        if cert:
            q.append(('D', f'{by} ((probe_ssl_earliest_cert_expiry{{{sel}}} - time()) / 86400)', False))
        targets = [{'refId': r, 'datasource': DS, 'expr': e, 'legendFormat': '__auto',
                    **({'range': True, 'instant': False, 'format': 'time_series'} if rng
                       else {'range': False, 'instant': True, 'format': 'table'})} for r, e, rng in q]
        rename = {'name': 'Endpoint', 'Value #E': 'Status', 'Trend #A': 'History', 'Trend #B': 'Response',
                  'Value #C': 'Uptime 24 h'}
        if cert:
            rename['Value #D'] = 'Certificate'
        order = ['Status', 'Endpoint', 'History', 'Response', 'Uptime 24 h'] + (['Certificate'] if cert else [])
        src = {v: k for k, v in rename.items()}

        def field(name, props):
            return {'matcher': {'id': 'byName', 'options': name}, 'properties': [{'id': k, 'value': v} for k, v in props]}

        def steps(*pairs):
            return {'mode': 'absolute', 'steps': [{'color': c, 'value': v} for v, c in pairs]}

        overrides = [
            field('Status', [('custom.cellOptions', {'type': 'color-text'}), ('custom.width', 90),
                             ('mappings', [{'type': 'value', 'options': {'0': {'text': '● DOWN', 'color': 'red', 'index': 0},
                                                                          '1': {'text': '● UP', 'color': 'green', 'index': 1}}}])]),
            field('Endpoint', [('custom.width', 200)]),
            field('History', [('custom.cellOptions', {'type': 'sparkline', 'hideValue': True}),
                              ('custom.drawStyle', 'bars'), ('custom.fillOpacity', 100), ('custom.lineWidth', 0),
                              ('custom.gradientMode', 'scheme'), ('color', {'mode': 'thresholds'}), ('min', 0), ('max', 1.5),
                              ('thresholds', steps((None, 'red'), (1, 'green')))]),
            field('Response', [('custom.cellOptions', {'type': 'sparkline'}), ('unit', 's'), ('custom.width', 220),
                               ('color', {'mode': 'fixed', 'fixedColor': 'blue'})]),
            field('Uptime 24 h', [('unit', 'percent'), ('decimals', 2), ('custom.width', 110), ('custom.cellOptions', {'type': 'color-text'}),
                                  ('color', {'mode': 'thresholds'}), ('thresholds', steps((None, 'red'), (99, 'orange'), (99.9, 'green')))]),
        ]
        if cert:
            overrides.append(field('Certificate', [('unit', 'suffix: days'), ('decimals', 0), ('custom.width', 110),
                                                   ('custom.cellOptions', {'type': 'color-text'}), ('color', {'mode': 'thresholds'}),
                                                   ('thresholds', steps((None, 'red'), (7, 'orange'), (14, 'green')))]))
        p = {'id': len(self.panels) + 1, 'type': 'table', 'title': title, 'datasource': DS, 'targets': targets,
             'gridPos': self._place(24, h),
             'transformations': [
                 {'id': 'timeSeriesTable', 'options': {'A': {'stat': 'lastNotNull', 'timeField': 'Time'},
                                                       'B': {'stat': 'lastNotNull', 'timeField': 'Time'}}},
                 {'id': 'merge', 'options': {}},
                 {'id': 'organize', 'options': {'excludeByName': {'Time': True},
                                                'renameByName': rename,
                                                'indexByName': {src[n]: i for i, n in enumerate(order)}}},
                 {'id': 'sortBy', 'options': {'sort': [{'field': 'Endpoint'}]}},
             ],
             'fieldConfig': {'defaults': {'custom': {'align': 'auto', 'cellOptions': {'type': 'auto'}, 'inspect': False},
                                          'thresholds': steps((None, 'green')), 'color': {'mode': 'thresholds'}},
                             'overrides': overrides},
             'options': {'showHeader': True, 'cellHeight': 'sm', 'footer': {'show': False}}}
        if desc:
            p['description'] = desc
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
b.stat('Unhealthy containers', f'count(returnit_container_healthy{{{APP}}} == 0) or vector(0)', thresholds=[(1, 'red')], decimals=0)
b.row('Speed and traffic')
b.graph('Response time (public, end to end)', 'probe_duration_seconds{job=~"blackbox-(http|redirect|apk)",name!="API (internal)"}',
        unit='s', legend='{{name}}')
b.graph('Requests per second by site', f'sum by (server) (rate({REQ}[5m]))', unit='reqps', legend='{{server}}')
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
b.graph('Temperatures', 'node_hwmon_temp_celsius{chip!~".*asus.*"} > -50 < 150', unit='celsius', legend='{{chip}} {{sensor}}',
        desc='The ASUS WMI sensor and an unused thermal zone report impossible values and are left out.')
b.graph('Battery and mains', ['node_power_supply_capacity{power_supply="BAT0"}', '100 * node_power_supply_online{power_supply="AC0"}'],
        unit='percent', legend=['battery %', 'mains (100 = on)'], minv=0, maxv=100)
b.write()

# 3. Containers
b = Board('returnit-containers', 'ReturnIt containers', 'Every container of the stack: CPU, memory, restarts, health, OOM kills.')
b.row('Problems')
b.stat('OOM kills (24 h)', f'sum(increase(container_oom_events_total{{{CG}}}[24h])) + sum(returnit_container_oom_killed{{{APP}}})', w=4,
       thresholds=[(1, 'red')], decimals=0, desc='Kernel OOM events in container cgroups, plus containers whose last exit was an OOM kill.')
b.stat('Restarts by Docker', f'sum(returnit_container_restart_count{{{APP}}})', w=4, thresholds=[(1, 'orange'), (5, 'red')], decimals=0,
       desc='Restarts done by the restart policy since each container was created (a crash loop shows up here).')
b.stat('Unhealthy', f'count(returnit_container_healthy{{{APP}}} == 0) or vector(0)', w=4, thresholds=[(1, 'red')], decimals=0)
b.stat('Running', f'sum(returnit_container_running{{{APP}}})', w=4)
b.stat('Stopped', f'count(returnit_container_running{{{APP},name!~".*migrate.*"}} == 0) or vector(0)', w=4, thresholds=[(1, 'orange')], decimals=0,
       desc='Containers that exist but are not running (the one-off migrate job is left out).')
b.stat('Memory of all containers', 'sum(' + named(f'container_memory_working_set_bytes{{{CG}}}') + ')', w=4, unit='bytes')
b.stat('Health checks', f'returnit_container_healthy{{{APP}}}', w=24, h=4, legend='{{name}}', base='red', thresholds=[(1, 'green')],
       mappings=[{'type': 'value', 'options': {'0': {'text': 'UNHEALTHY', 'color': 'red', 'index': 0}, '1': {'text': 'HEALTHY', 'color': 'green', 'index': 1}}}])
b.row('Per container')
b.graph('CPU (1.0 = one full core)', 'sum by (name) (' + named(f'rate(container_cpu_usage_seconds_total{{{CG}}}[5m])') + ')', unit='percentunit', legend='{{name}}')
b.graph('Memory', named(f'container_memory_working_set_bytes{{{CG}}}'), unit='bytes', legend='{{name}}')
b.panel('bargauge', 'Memory against limit (limited containers)',
        '100 * ' + named(f'container_memory_working_set_bytes{{{CG}}}') + ' / ' + named(f'(container_spec_memory_limit_bytes{{{CG}}} > 0 < 1e15)'),
        w=12, h=8, unit='percent', minv=0, maxv=100, legend='{{name}}', thresholds=[(80, 'orange'), (95, 'red')])
b.graph('OOM events per container', named(f'increase(container_oom_events_total{{{CG}}}[1h])'), legend='{{name}}', decimals=0)
b.graph('Restarts by Docker', f'returnit_container_restart_count{{{APP}}}', legend='{{name}}', decimals=0)
b.graph('Running for', named(f'time() - container_start_time_seconds{{{CG}}}'), unit='s', legend='{{name}}')
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
b.graph('Requests per second', f'sum by (server) (rate({REQ}[5m]))', unit='reqps', legend='{{server}}')
b.graph('Status codes', f'sum by (code) (rate({REQ}[5m]))', unit='reqps', legend='{{code}}', stack=True)
b.graph('Latency p50', f'histogram_quantile(0.5, sum by (le, server) (rate(caddy_http_request_duration_seconds_bucket{{{CADDY}}}[5m])))', unit='s', legend='{{server}}')
b.graph('Latency p95', f'histogram_quantile(0.95, sum by (le, server) (rate(caddy_http_request_duration_seconds_bucket{{{CADDY}}}[5m])))', unit='s', legend='{{server}}')
b.graph('5xx per site', f'sum by (server) (rate(caddy_http_request_duration_seconds_count{{{CADDY},code=~"5.."}}[5m]))', unit='reqps', legend='{{server}}')
b.graph('Bytes sent per site', f'sum by (server) (rate(caddy_http_response_size_bytes_sum{{{CADDY}}}[5m]))', unit='Bps', legend='{{server}}')
b.write()

# 5. Uptime (blackbox): one table per group, like a status page
PUB = 'job=~"blackbox-(http|redirect|apk)",name!="API (internal)"'
INT = 'name=~"API \\\\(internal\\\\)|Postgres|Redis|MinIO|Keycloak"'
NET = 'job="blackbox-icmp"'
b = Board('returnit-uptime', 'ReturnIt uptime', 'Probes from the server every minute: public sites through Cloudflare, internal services, router, internet.')
b.stat('Endpoints failing', 'count(probe_success == 0) or vector(0)', w=6, h=5, thresholds=[(1, 'red')], decimals=0)
b.stat('Endpoints slow (> 3 s)', 'count(probe_duration_seconds > 3) or vector(0)', w=6, h=5, thresholds=[(1, 'orange')], decimals=0)
b.stat('Certificates ending < 14 days', 'count((probe_ssl_earliest_cert_expiry - time()) / 86400 < 14) or vector(0)', w=6, h=5,
       thresholds=[(1, 'orange')], decimals=0, desc='Cloudflare renews the edge certificates by itself; this only warns if it does not.')
b.panel('stat', 'Endpoints up / watched', ['sum(probe_success)', 'count(probe_success)'], w=6, h=5, legend=['up', 'watched'], decimals=0)
b.probe_table('Public (through Cloudflare, as users reach it)', PUB, h=8)
b.probe_table('Internal services (on the server, no Cloudflare)', INT, h=7, cert=False)
b.probe_table('Network (Wi-Fi to the router, and the internet)', NET, h=4, cert=False,
              desc='A failing router ping points at the Wi-Fi; a failing internet ping with a working router points at the ISP.')
b.row('Details')
b.graph('Response time per endpoint', [f'probe_duration_seconds{{{PUB}}}', f'probe_duration_seconds{{{INT}}}'], w=24, h=10, unit='s',
        legend=['{{name}}', '{{name}}'], legend_table=['mean', 'max', 'lastNotNull'],
        desc='Public endpoints go through Cloudflare and back (colours); internal services are probed on the server (blue).',
        overrides=[{'matcher': {'id': 'byFrameRefID', 'options': 'B'},
                    'properties': [{'id': 'color', 'value': {'mode': 'fixed', 'fixedColor': 'blue'}}]}])
b.panel('bargauge', 'Certificate days left', f'max by (name) ((probe_ssl_earliest_cert_expiry{{{PUB}}} - time()) / 86400)', w=24, h=7,
        unit='suffix: days', decimals=0, minv=0, maxv=90, legend='{{name}}', bars='basic', base='red', thresholds=[(7, 'orange'), (14, 'green')],
        desc='Edge certificates of Cloudflare, renewed by Cloudflare. Orange below 14 days, red below 7.')
b.graph('Ping', 'probe_icmp_duration_seconds{phase="rtt"}', unit='s', legend='{{name}}', legend_table=['mean', 'max', 'lastNotNull'])
b.graph('DNS lookup', f'probe_dns_lookup_time_seconds{{{PUB}}}', unit='s', legend='{{name}}', legend_table=['mean', 'max', 'lastNotNull'])
b.graph('HTTP status returned', f'probe_http_status_code{{{PUB}}}', w=24, h=6, legend='{{name}}', decimals=0)
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
b.stat('Stored', 'max(minio_cluster_usage_total_bytes)', w=8, unit='bytes')
b.stat('Files', 'max(minio_cluster_usage_object_total)', w=8)
b.stat('Free space', 'max(minio_cluster_capacity_usable_free_bytes)', w=8, unit='bytes')
b.graph('Stored per bucket', 'minio_bucket_usage_total_bytes', unit='bytes', legend='{{bucket}}')
b.graph('Files per bucket', 'minio_bucket_usage_object_total', legend='{{bucket}}', decimals=0)
b.write()
