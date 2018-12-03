package conf

import (
	config "github.com/spf13/viper"
)

func init() {

	// Logger Defaults
	config.SetDefault("logger.level", "info")
	config.SetDefault("logger.encoding", "console")
	config.SetDefault("logger.color", true)
	config.SetDefault("logger.dev_mode", true)
	config.SetDefault("logger.disable_caller", false)
	config.SetDefault("logger.disable_stacktrace", true)

	// Pidfile
	config.SetDefault("pidfile", "")

	// Profiler config
	config.SetDefault("profiler.enabled", false)
	config.SetDefault("profiler.host", "")
	config.SetDefault("profiler.port", "6060")

	// Server Configuration
	config.SetDefault("server.host", "")
	config.SetDefault("server.port", "8900")
	config.SetDefault("server.tls", false)
	config.SetDefault("server.devcert", false)
	config.SetDefault("server.certfile", "server.crt")
	config.SetDefault("server.keyfile", "server.key")
	config.SetDefault("server.log_requests", true)
	config.SetDefault("server.profiler_enabled", false)
	config.SetDefault("server.profiler_path", "/debug")
	// GRPC JSON Marshaler Options
	config.SetDefault("server.rest.enums_as_ints", false)
	config.SetDefault("server.rest.emit_defaults", true)
	config.SetDefault("server.rest.orig_names", true)

	// Database Settings
	config.SetDefault("storage.type", "postgres")
	config.SetDefault("storage.username", "postgres")
	config.SetDefault("storage.password", "password")
	config.SetDefault("storage.host", "postgres")
	config.SetDefault("storage.port", 5432)
	config.SetDefault("storage.database", "gogrpcapi")
	config.SetDefault("storage.sslmode", "disable")
	config.SetDefault("storage.retries", 5)
	config.SetDefault("storage.sleep_between_retries", "7s")
	config.SetDefault("storage.max_connections", 80)
	config.SetDefault("storage.wipe_confirm", false)

	// Set Defaults - Elasticsearch
	config.SetDefault("elasticsearch.mapping_file", "./mapping.json")
	config.SetDefault("elasticsearch.request_log", false)
	config.SetDefault("elasticsearch.debug", false)
	config.SetDefault("elasticsearch.sniff", true)
	config.SetDefault("elasticsearch.host", "elasticsearch")
	config.SetDefault("elasticsearch.port", "9200")
	config.SetDefault("elasticsearch.retries", 5)
	config.SetDefault("elasticsearch.sleep_between_retries", "5s")
	config.SetDefault("elasticsearch.index", "btcc")
	config.SetDefault("elasticsearch.index_replicas", 0)
	config.SetDefault("elasticsearch.index_shards", 5)
	config.SetDefault("elasticsearch.refresh_interval", "30s")
	config.SetDefault("elasticsearch.wipe_confirm", false)

	// Bitcoin network settings
	config.SetDefault("bitcoind.host", "bitcoind")
	config.SetDefault("bitcoind.port", 8333)
	config.SetDefault("bitcoind.chain", "mainnet")
}
