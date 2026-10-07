use std::{error::Error, time::Duration};
use tonic::transport::{Certificate, Identity, ServerTlsConfig};

fn env(name: &str) -> Result<String, std::env::VarError> {
    match std::env::var(name) {
        Err(std::env::VarError::NotPresent) => Ok(String::new()),
        result => result,
    }
}

pub fn from_env() -> Result<Option<ServerTlsConfig>, Box<dyn Error>> {
    config(
        &env("JANSETU_ENV")?,
        &env("JANSETU_RECOMMENDATION_TLS_CA_FILE")?,
        &env("JANSETU_RECOMMENDATION_TLS_CERT_FILE")?,
        &env("JANSETU_RECOMMENDATION_TLS_KEY_FILE")?,
    )
}

fn config(
    environment: &str,
    ca: &str,
    cert: &str,
    key: &str,
) -> Result<Option<ServerTlsConfig>, Box<dyn Error>> {
    if ca.is_empty() && cert.is_empty() && key.is_empty() {
        if matches!(environment, "" | "local" | "test") {
            return Ok(None);
        }
        return Err("recommendation TLS is required outside local/test mode".into());
    }
    if ca.is_empty() || cert.is_empty() || key.is_empty() {
        return Err("recommendation TLS requires client CA, server certificate and key".into());
    }
    Ok(Some(
        ServerTlsConfig::new()
            .identity(Identity::from_pem(
                std::fs::read(cert)?,
                std::fs::read(key)?,
            ))
            .client_ca_root(Certificate::from_pem(std::fs::read(ca)?))
            .client_auth_optional(false)
            .timeout(Duration::from_secs(3)),
    ))
}

#[cfg(test)]
mod tests {
    use super::config;

    #[test]
    fn plaintext_is_local_only_and_partial_tls_never_downgrades() {
        for environment in ["", "local", "test"] {
            assert!(config(environment, "", "", "").unwrap().is_none());
        }
        for environment in ["production", "staging", "typo"] {
            assert!(config(environment, "", "", "").is_err());
        }
        for (ca, cert, key) in [
            ("ca", "", ""),
            ("", "cert", ""),
            ("", "", "key"),
            ("ca", "cert", ""),
            ("ca", "", "key"),
            ("", "cert", "key"),
        ] {
            assert!(config("local", ca, cert, key).is_err());
        }
    }
}
