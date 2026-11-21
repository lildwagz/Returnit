#!/bin/sh
# Writes container facts for node_exporter's textfile collector every minute (run by the container-info service in
# docker-compose.monitoring.yml, which mounts the Docker socket read-only and the textfile volume at /out):
#   returnit_container_info{id,name}           maps cAdvisor's cgroup id to the container name (dashboards join on it)
#   returnit_container_restart_count{name}     restarts done by Docker's restart policy
#   returnit_container_oom_killed{name}        1 when the last exit was an out-of-memory kill
#   returnit_container_running{name}           1 while running
#   returnit_container_healthy{name}           1 healthy, 0 unhealthy or starting, absent without a health check
# cAdvisor cannot read container names under Docker 29's containerd image store, so it reports cgroups only.
# Health is read with `index`: Docker 29's CLI refuses `.State.Health` on containers that have no health check.
set -eu
out=/out/containers.prom
while true; do
	ids=$(docker ps -aq 2>/dev/null || true)
	if [ -n "$ids" ]; then
		# shellcheck disable=SC2086
		docker inspect --format '{{.Id}}|{{.Name}}|{{.RestartCount}}|{{.State.OOMKilled}}|{{.State.Running}}|{{with index .State "Health"}}{{.Status}}{{end}}' $ids 2>/dev/null |
			awk -F'|' '
				BEGIN {
					print "# HELP returnit_container_info Docker container cgroup id to name."
					print "# TYPE returnit_container_info gauge"
				}
				{
					name = $2; sub(/^\//, "", name)
					printf "returnit_container_info{id=\"/system.slice/docker-%s.scope\",name=\"%s\"} 1\n", $1, name
					restarts[name] = $3; oom[name] = ($4 == "true"); run[name] = ($5 == "true"); health[name] = $6
				}
				END {
					print "# TYPE returnit_container_restart_count gauge"
					for (n in restarts) printf "returnit_container_restart_count{name=\"%s\"} %d\n", n, restarts[n]
					print "# TYPE returnit_container_oom_killed gauge"
					for (n in oom) printf "returnit_container_oom_killed{name=\"%s\"} %d\n", n, oom[n]
					print "# TYPE returnit_container_running gauge"
					for (n in run) printf "returnit_container_running{name=\"%s\"} %d\n", n, run[n]
					print "# TYPE returnit_container_healthy gauge"
					for (n in health) if (health[n] != "") printf "returnit_container_healthy{name=\"%s\"} %d\n", n, (health[n] == "healthy")
				}' >"$out.tmp" && mv "$out.tmp" "$out"
	fi
	sleep 60
done
