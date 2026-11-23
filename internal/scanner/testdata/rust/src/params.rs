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
