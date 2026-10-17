#!/usr/bin/env bash
# Refreshes the COPIES inside Docs/mobile/ from their originals. Run from anywhere:
#   bash scripts/sync-mobile-docs.sh
# Edit the originals, never the copies. (01-mobile-app-guide.md and 02-screens-and-api-map.md have no
# original: they live only in Docs/mobile/.)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../Docs"

copy() {
	mkdir -p "$(dirname "mobile/$2")"
	cp "$1" "mobile/$2"
	echo "updated mobile/$2"
}

copy api/openapi.yaml 03-openapi.yaml
copy ReturnIt_Paket_UIUX_Visual.docx 04-design/ReturnIt_Paket_UIUX_Visual.docx
copy ReturnIt_Interactive_Prototype.html 04-design/ReturnIt_Interactive_Prototype.html
copy ReturnIt_SRS_Requirements_UseCases_BPMN.docx 05-requirements/ReturnIt_SRS_Requirements_UseCases_BPMN.docx
copy docs/data-retention-policy.md 06-data-and-privacy/data-retention-policy.md
