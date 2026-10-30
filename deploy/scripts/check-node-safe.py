#!/usr/bin/env python3
import json, pathlib, sys
p=pathlib.Path(sys.argv[1])
if not p.is_file(): sys.exit("BLOCK: journal missing")
data=json.loads(p.read_text())
if not isinstance(data.get("sandboxes"),dict) or not isinstance(data.get("operations"),dict):sys.exit("BLOCK: unknown journal")
for key,record in data["sandboxes"].items():
    if record.get("state") not in ("stopped","deleted","failed"):sys.exit("BLOCK: active sandbox "+key)
for key,entry in data["operations"].items():
    if entry.get("operation",{}).get("state") not in (3,4,"OPERATION_STATE_SUCCEEDED","OPERATION_STATE_FAILED"):sys.exit("BLOCK: pending operation "+key)
print("SAFE: no active VM in journal")
