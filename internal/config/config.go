package config

// ServerConfig defines server settings.
type ServerConfig struct {
	Addr      string
	AuthToken string
}

// AgentConfig defines agent settings.
type AgentConfig struct {
	ServerURL    string
	AgentID      string
	AuthToken    string
	Capabilities []string
}

// TODO: Candidate can add config loading from flags/env/file.
