//! Fixture crate for the Rust intake: tools spread over several files and
//! registration styles. It is never compiled.

mod params;
mod raw;

use rmcp::{handler::server::wrapper::Parameters, tool, tool_router};

use crate::params::{DeleteFileParams, GetWeatherParams};

#[derive(Clone)]
pub struct Server;

#[tool_router]
impl Server {
    /// Get the current weather for a city.
    #[tool(annotations(read_only_hint = true))]
    async fn get_weather(&self, Parameters(p): Parameters<GetWeatherParams>) -> String {
        format!("Sunny in {}", p.city)
    }

    #[tool(
        name = "delete_file",
        description = "Delete a file inside the sandbox directory.",
        annotations(destructive_hint = true)
    )]
    async fn remove(
        &self,
        Parameters(DeleteFileParams { path, confirm }): Parameters<DeleteFileParams>,
    ) -> String {
        format!("would delete {path} ({confirm})")
    }
}
