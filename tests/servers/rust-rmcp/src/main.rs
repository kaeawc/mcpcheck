use std::sync::Arc;

use rmcp::{
    ServerHandler, ServiceExt,
    handler::server::wrapper::Parameters,
    tool, tool_handler, tool_router,
    transport::{
        StreamableHttpServerConfig, StreamableHttpService,
        streamable_http_server::session::local::LocalSessionManager,
    },
};
use schemars::JsonSchema;
use serde::Deserialize;

#[derive(Debug, Deserialize, JsonSchema)]
#[serde(rename_all = "lowercase")]
pub enum Units {
    Celsius,
    Fahrenheit,
}

#[derive(Debug, Deserialize, JsonSchema)]
pub struct GetWeatherParams {
    /// City name
    pub city: String,
    pub units: Option<Units>,
}

#[derive(Debug, Deserialize, JsonSchema)]
pub struct DeleteFileParams {
    pub path: String,
    pub confirm: bool,
}

#[derive(Debug, Deserialize, JsonSchema)]
pub struct FetchUrlParams {
    pub url: String,
}

#[derive(Debug, Clone, Default)]
pub struct Server;

#[tool_router]
impl Server {
    #[tool(
        name = "get_weather",
        description = "Get the current weather for a city.",
        annotations(read_only_hint = true)
    )]
    async fn get_weather(&self, Parameters(p): Parameters<GetWeatherParams>) -> String {
        format!("Sunny in {}", p.city)
    }

    #[tool(
        name = "delete_file",
        description = "Delete a file inside the sandbox directory.",
        annotations(destructive_hint = true)
    )]
    async fn delete_file(&self, Parameters(p): Parameters<DeleteFileParams>) -> String {
        format!("would delete {}", p.path)
    }

    #[tool(
        name = "fetch_url",
        description = "Fetch a URL and return the response body.",
        annotations(open_world_hint = true)
    )]
    async fn fetch_url(&self, Parameters(p): Parameters<FetchUrlParams>) -> String {
        format!("would fetch {}", p.url)
    }
}

#[tool_handler(name = "mcpcheck-rust-rmcp", version = "0.1.0")]
impl ServerHandler for Server {}

fn usage() -> ! {
    eprintln!("usage: rust-rmcp stdio | rust-rmcp http --port N");
    std::process::exit(2);
}

fn parse_port(args: &[String]) -> u16 {
    match args {
        [flag, port] if flag == "--port" => port.parse().unwrap_or_else(|_| usage()),
        _ => usage(),
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match args.first().map(String::as_str) {
        Some("stdio") => {
            let service = Server.serve(rmcp::transport::stdio()).await?;
            service.waiting().await?;
        }
        Some("http") => {
            let port = parse_port(&args[1..]);
            let service = StreamableHttpService::new(
                || Ok(Server),
                Arc::new(LocalSessionManager::default()),
                StreamableHttpServerConfig::default(),
            );
            let router = axum::Router::new().nest_service("/mcp", service);
            let listener = tokio::net::TcpListener::bind(("127.0.0.1", port)).await?;
            eprintln!("rust-rmcp listening on http://127.0.0.1:{port}/mcp");
            axum::serve(listener, router)
                .with_graceful_shutdown(async {
                    let _ = tokio::signal::ctrl_c().await;
                })
                .await?;
        }
        _ => usage(),
    }
    Ok(())
}
