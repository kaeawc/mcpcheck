use serde_json::{Value, json};

/// Hand-rolled tools/list result.
pub fn list_tools() -> Value {
    json!({
        "tools": [{
            "name": "fetch_url",
            "description": "Fetch a URL and return the response body.",
            "inputSchema": {
                "type": "object",
                "properties": {"url": {"type": "string", "format": "uri"}},
                "required": ["url"]
            },
            "annotations": {"openWorldHint": true}
        }]
    })
}
