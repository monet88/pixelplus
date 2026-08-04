"""Check the SSE chat stream wire structs against the frozen OpenAPI schemas.

Validates that every emitted JSON field is declared by the published schema
(`additionalProperties: false`) and that every `required` field is present.
Run from the repository root: `python scripts/check-chat-stream-wire.py`.
"""

import json
import re
import sys

SPEC = "contracts/openapi/pixelplus-public-api-v1.yaml"
SOURCE = "apps/gateway/internal/transport/http/chat_stream_wire.go"

MAPPING = {
    "chatOpenEventWire": "ChatOpenEvent",
    "chatDeltaEventWire": "ChatDeltaEvent",
    "chatHeartbeatEventWire": "ChatHeartbeatEvent",
    "chatCompletedEventWire": "ChatCompletedEvent",
    "chatFailedEventWire": "ChatFailedEvent",
    "chatCanceledEventWire": "ChatCanceledEvent",
    "chatSafeMetadataWire": "ChatSafeMetadata",
    "chatDeltaChoiceWire": "ChatDeltaChoice",
}


def main() -> int:
    with open(SPEC, encoding="utf-8") as handle:
        schemas = json.load(handle)["components"]["schemas"]
    with open(SOURCE, encoding="utf-8") as handle:
        source = handle.read()

    structs = dict(re.findall(r"type (chat\w+Wire) struct \{(.*?)\n\}", source, re.S))
    failures = 0
    for go_name, schema_name in MAPPING.items():
        body = structs.get(go_name, "")
        if not body:
            print("MISSING STRUCT", go_name)
            failures += 1
            continue
        tags = [tag.split(",")[0] for tag in re.findall(r'json:"([^"]+)"', body)]
        schema = schemas[schema_name]
        declared = set(schema.get("properties", {}).keys())
        required = set(schema.get("required", []))
        extra = set(tags) - declared
        missing = required - set(tags)
        if extra or missing:
            failures += 1
            print("PROBLEM", go_name, "->", schema_name)
            if extra:
                print("   undeclared fields (additionalProperties:false):", sorted(extra))
            if missing:
                print("   missing required fields:", sorted(missing))
        else:
            print("OK     ", go_name, "->", schema_name)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
