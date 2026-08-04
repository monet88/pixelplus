"""Check the SSE chat stream wire structs against the frozen OpenAPI schemas.

Validates that every emitted JSON field is declared by the published schema
(`additionalProperties: false`) and that every `required` field is present.

Coverage note: the event envelopes are not enough. The leaf structs nested inside
them carry the actual user-visible payload (`delta.content`, the token counts, the
canonical error), so schema drift in a leaf would otherwise pass while the emitted
bytes violate `additionalProperties: false`. Every struct reachable from an event
is therefore mapped, including leaves that live in sibling wire files.

Run from the repository root: `python scripts/check-chat-stream-wire.py`.
"""

import json
import re
import sys

SPEC = "contracts/openapi/pixelplus-public-api-v1.yaml"

# Wire structs are split across files: the stream events are their own file, but
# the usage and canonical-error leaves are shared with the non-streaming spine.
SOURCES = (
    "apps/gateway/internal/transport/http/chat_stream_wire.go",
    "apps/gateway/internal/transport/http/chat_wire.go",
    "apps/gateway/internal/transport/http/provideraccount_wire.go",
)

# Go struct name -> schema name in components.schemas.
MAPPING = {
    # Event envelopes.
    "chatOpenEventWire": "ChatOpenEvent",
    "chatDeltaEventWire": "ChatDeltaEvent",
    "chatHeartbeatEventWire": "ChatHeartbeatEvent",
    "chatCompletedEventWire": "ChatCompletedEvent",
    "chatFailedEventWire": "ChatFailedEvent",
    "chatCanceledEventWire": "ChatCanceledEvent",
    # Leaves reachable from those envelopes.
    "chatSafeMetadataWire": "ChatSafeMetadata",
    "chatDeltaChoiceWire": "ChatDeltaChoice",
    "usageWire": "ChatUsage",
    "canonicalErrorWire": "CanonicalError",
}

# Leaves whose schema is declared inline (no $ref), addressed by a property path
# from a named schema: Go struct name -> (schema name, property name).
INLINE_MAPPING = {
    "chatDeltaBodyWire": ("ChatDeltaChoice", "delta"),
}


def parse_structs(sources):
    """Return {go struct name: body} for every `type XxxWire struct` in sources."""
    structs = {}
    for path in sources:
        with open(path, encoding="utf-8") as handle:
            source = handle.read()
        for name, body in re.findall(r"type (\w+Wire) struct \{(.*?)\n\}", source, re.S):
            structs[name] = body
    return structs


def json_tags(body):
    """Emitted JSON field names, ignoring option suffixes such as `,omitempty`."""
    return [tag.split(",")[0] for tag in re.findall(r'json:"([^"]+)"', body)]


def check(go_name, label, body, schema):
    """Compare emitted tags against one schema object. Returns True on failure."""
    tags = json_tags(body)
    declared = set(schema.get("properties", {}).keys())
    required = set(schema.get("required", []))
    extra = set(tags) - declared
    missing = required - set(tags)
    if extra or missing:
        print("PROBLEM", go_name, "->", label)
        if extra:
            print("   undeclared fields (additionalProperties:false):", sorted(extra))
        if missing:
            print("   missing required fields:", sorted(missing))
        return True
    print("OK     ", go_name, "->", label)
    return False


def main() -> int:
    with open(SPEC, encoding="utf-8") as handle:
        # The published artifact carries a .yaml extension but is JSON, which is
        # valid YAML. Parse it as JSON so the check needs no third-party YAML
        # dependency; a genuinely non-JSON spec fails loudly here rather than
        # silently skipping validation.
        try:
            spec = json.load(handle)
        except json.JSONDecodeError as err:
            print("CANNOT PARSE SPEC", SPEC, "->", err)
            print("   the artifact is no longer JSON-compatible; add a YAML parser here")
            return 1
    schemas = spec["components"]["schemas"]

    structs = parse_structs(SOURCES)
    failures = 0

    for go_name, schema_name in MAPPING.items():
        body = structs.get(go_name)
        if body is None:
            print("MISSING STRUCT", go_name)
            failures += 1
            continue
        if schema_name not in schemas:
            print("MISSING SCHEMA", schema_name, "for", go_name)
            failures += 1
            continue
        if check(go_name, schema_name, body, schemas[schema_name]):
            failures += 1

    for go_name, (schema_name, prop) in INLINE_MAPPING.items():
        body = structs.get(go_name)
        if body is None:
            print("MISSING STRUCT", go_name)
            failures += 1
            continue
        inline = schemas.get(schema_name, {}).get("properties", {}).get(prop)
        if not isinstance(inline, dict) or "properties" not in inline:
            print("MISSING INLINE SCHEMA", f"{schema_name}.{prop}", "for", go_name)
            failures += 1
            continue
        if check(go_name, f"{schema_name}.{prop}", body, inline):
            failures += 1

    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
