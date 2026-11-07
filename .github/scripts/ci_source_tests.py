"""Reviewed source-to-test ownership; lists contain exact files, never prefixes."""

SOURCE_TEST_FILES = {}


def group(directory: str, sources: str, tests: str) -> None:
    for source in sources.split():
        path = directory + source + ".go"
        selected = SOURCE_TEST_FILES.setdefault(path, [])
        for test in tests.split():
            target = directory + test + "_test.go"
            if target not in selected:
                selected.append(target)


SERVICE = "backend/internal/service/"
# These names were reviewed for v0.2.20; new shared files do not inherit a rule.
for source in """
admin_user_email_blacklist anthropic_messages_url antigravity_gateway_compat
bedrock_stream billing_notification_worker custom_admin_key_accounts
custom_admin_key_statistics custom_admin_keys digest_session_store
mcp_oauth_service openai_codex_model_metadata openai_codex_models_service
openai_compact_body_signal openai_compat_prompt_cache_key
openai_cyber_session_block openai_gateway_anthropic_native_pump
openai_gateway_grok_model_input openai_gateway_grok_sse_filter
openai_gateway_grok_tool_protocol openai_gateway_messages_anthropic_native
openai_gateway_responses_chat_fallback openai_image_size_routing
openai_referral_service openai_responses_instructions_echo
openai_responses_orphan_precheck openai_ws_http_bridge
registration_email_blacklist session_memory_cache sse_transport_limits
""".split():
    group(SERVICE, source, source)

group(SERVICE, "account_test_service",
      "anthropic_messages_url account_test_service_openai_image account_test_service_antigravity")
group(SERVICE, "account_usage_service",
      "account_usage_service_setup_token_weekly antigravity_quota_groups custom_admin_key_statistics")
group(SERVICE, "admin_service", "admin_user_email_blacklist")
group(SERVICE, """
antigravity_gateway_compat_stream antigravity_gateway_streaming
antigravity_gateway_upstream gateway_forward_as_chat_completions
gateway_forward_as_responses gateway_forward_compat_stream
gateway_upstream_response openai_gateway_cc_pipeline
openai_gateway_chat_completions_anthropic_native
openai_gateway_responses_anthropic_native
""", "sse_transport_limits")
group(SERVICE, "antigravity_gateway_gemini", "antigravity_gemini_response_schema")
group(SERVICE, "antigravity_gateway_retry antigravity_internal500_penalty", "antigravity_internal500_scope")
group(SERVICE, "antigravity_gateway_service", "account_test_service_antigravity")
group(SERVICE, "antigravity_quota_fetcher", "antigravity_quota_groups antigravity_quota_fetcher")
group(SERVICE, "api_key_auth_cache api_key_auth_cache_impl", "billing_cache_service_rpm openai_privacy_gate_ctx")
group(SERVICE, "api_key_service", "api_key_service_cache")
group(SERVICE, "auth_email_oauth_auto", "auth_email_oauth_blacklist")
group(SERVICE, "auth_service", "auth_registration_email_blacklist mcp_oauth_service")
group(SERVICE, "balance_notify_service", "billing_notification_worker balance_notify_check")
group(SERVICE, "billing_cache_service", "billing_cache_service_rpm")
group(SERVICE, "billing_service pricing_service", "openai_images26 openai_images_model")
group(SERVICE, "custom_admin_key_openai_options group_capacity_service", "custom_admin_key_permissions")
group(SERVICE, "domain_constants setting_service", "registration_email_blacklist")
group(SERVICE, "gateway_anthropic_passthrough", "anthropic_messages_url sse_transport_limits")
group(SERVICE, "gateway_claude_oauth_body", "gateway_cache_control_paths")
group(SERVICE, "gateway_request", "gateway_request_reuse gateway_request_scalar_memory gateway_thinking_filter_precheck")
group(SERVICE, "gateway_upstream_request", "anthropic_messages_url")
group(SERVICE, "gateway_usage_billing", "gateway_usage_quota_worker billing_notification_worker")
group(SERVICE, "gemini_chat_completions_compat_service", "gemini_compat_thinking gemini_sse_line_limit")
group(SERVICE, "gemini_messages_compat_service", "gemini_messages_compat_service gemini_native_response_signal gemini_text_delta")
group(SERVICE, "grok_quota_service openai_quota_service", "custom_admin_key_statistics")
group(SERVICE, "openai_account_scheduler openai_gateway_scheduling", "openai_image_size_routing openai_privacy_gate_ctx")
group(SERVICE, "openai_apikey_responses_probe", "openai_apikey_responses_probe")
group(SERVICE, "openai_cyber_transcript", "openai_cyber_session_block")
group(SERVICE, "openai_gateway_chat_completions openai_gateway_messages",
      "sse_transport_limits openai_gpt61_terra openai_gateway_buffered_reasoning_content")
group(SERVICE, "openai_gateway_chat_completions_raw openai_gateway_messages_chat_fallback", "openai_gpt61_terra")
group(SERVICE, "openai_gateway_forward", "openai_request_patches openai_responses_instructions_echo")
group(SERVICE, "openai_gateway_grok", "grok_sse_transport_config openai_gateway_grok")
group(SERVICE, "openai_gateway_passthrough openai_gateway_response_handling",
      "sse_transport_limits openai_responses_instructions_echo openai_oauth_passthrough_non_stream")
group(SERVICE, "openai_gateway_request_body", "openai_request_patches openai_passthrough_normalization openai_gpt61_terra")
group(SERVICE, "openai_gateway_service openai_messages_continuation", "session_memory_cache")
group(SERVICE, "openai_gateway_usage", "openai_subscription_long_context")
group(SERVICE, "openai_image_dimensions", "openai_images26")
group(SERVICE, "openai_images", "openai_images26 openai_images_stream_limit_error openai_images_model")
group(SERVICE, "openai_images_direct", "openai_images_direct openai_images_direct_payload")
group(SERVICE, "openai_images_responses", "openai_images26 openai_images_stream_limit_error")
group(SERVICE, "openai_messages_digest_session", "session_memory_cache digest_session_store")
group(SERVICE, "openai_sse_data", "openai_sse_concatenated_json")
group(SERVICE, "openai_upstream_transport_error", "openai_upstream_transport_error_handle")
group(SERVICE, "openai_ws_v2/passthrough_relay", "openai_ws_v2/passthrough_relay")
group(SERVICE, "ratelimit_cn_providers", "ratelimit_cn_concurrency_403")
group(SERVICE, "ratelimit_service", "ratelimit_weekly_usage ratelimit_service_cn_quota_403")
group(SERVICE, "setting_public settings_view", "setting_service_public")
group(SERVICE, "user", "billing_cache_service_rpm")

INTERNAL = "backend/internal/"
group(INTERNAL, """
handler/admin/account_handler handler/admin/custom_admin_key_openai
handler/admin/custom_admin_key_statistics handler/admin/openai_oauth_handler
handler/admin/openai_referral_handler
""", "handler/admin/custom_admin_key_statistics")
group(INTERNAL, "handler/admin/custom_admin_key_accounts",
      "handler/admin/custom_admin_key_accounts handler/admin/custom_admin_key_permissions")
group(INTERNAL, "handler/admin/custom_admin_key_group_models", "handler/admin/custom_admin_key_permissions")
group(INTERNAL, "handler/admin/custom_admin_key_proxies", "handler/admin/custom_admin_key_proxies")
group(INTERNAL, "handler/admin/custom_admin_key_users handler/admin/user_handler", "handler/admin/custom_admin_key_users")
group(INTERNAL, "handler/admin/custom_admin_keys", "handler/admin/custom_admin_keys server/routes/custom_admin_key_full_access")
group(INTERNAL, "handler/admin/idempotency_helper", "handler/admin/custom_admin_key_idempotency_scope")
group(INTERNAL, "handler/admin/scoped_admin_content_response", "handler/admin/scoped_admin_content_response")
group(INTERNAL, "handler/admin/scoped_admin_subscription_response", "handler/admin/scoped_admin_subscription_response")
group(INTERNAL, "handler/admin/scoped_admin_usage_response", "handler/admin/scoped_admin_usage_response")
group(INTERNAL, "handler/admin/setting_handler_email_blacklist handler/admin/user_handler_email_blacklist",
      "handler/admin/email_blacklist_handler")
group(INTERNAL, "handler/admin/setting_handler_update", "handler/admin/setting_handler_stepup_switch")
group(INTERNAL, "handler/admin/audit_log_handler", "server/middleware/step_up")
group(INTERNAL, """
handler/gateway_handler handler/gateway_handler_chat_completions handler/gateway_handler_responses
handler/gateway_systemone handler/gemini_v1beta_handler handler/grok_media
handler/openai_alpha_search handler/openai_chat_completions handler/openai_embeddings
""", "handler/request_body_read_log handler/usage_record_async_boundary")
group(INTERNAL, "handler/gateway_handler handler/gateway_handler_chat_completions handler/gateway_handler_responses",
      "handler/usage_record_context_reuse")
group(INTERNAL, "handler/grok_audio", "handler/grok_audio_body handler/usage_record_async_boundary")
group(INTERNAL, "handler/gemini_cached_contents_handler handler/openai_gateway_count_tokens", "handler/request_body_read_log")
group(INTERNAL, "handler/image_task_handler", "handler/image_task_handler")
group(INTERNAL, "handler/mcp_oauth_handler", "handler/mcp_oauth_handler")
group(INTERNAL, "handler/openai_gateway_handler",
      "handler/openai_request_body_memory handler/openai_body_limit_passthrough_rule handler/openai_gateway_cyber handler/openai_automation_bootstrap")
group(INTERNAL, "handler/openai_images", "handler/openai_images_size_routing handler/usage_record_async_boundary")
group(INTERNAL, "handler/ops_error_logger", "handler/ops_error_logger")
group(INTERNAL, "handler/request_body_limit", "handler/request_body_limit")
group(INTERNAL, "handler/request_body_read_log", "handler/request_body_read_log")
group(INTERNAL, "handler/setting_handler", "handler/setting_handler_public")

group(INTERNAL, "repository/account_repo", "repository/custom_admin_key_scope")
group(INTERNAL, "repository/api_key_repo", "repository/api_key_repo_profit_projection_integration")
group(INTERNAL, "repository/custom_admin_key_group_capacity repository/custom_admin_key_group_models repository/custom_admin_key_scope",
      "repository/custom_admin_key_permissions")
group(INTERNAL, "repository/custom_admin_keys", "repository/custom_admin_keys repository/custom_admin_keys_postgres")
group(INTERNAL, "repository/gateway_cache", "repository/gateway_cache_cyber")
group(INTERNAL, "repository/http_upstream", "repository/http_upstream_drain")
group(INTERNAL, "repository/mcp_oauth", "repository/mcp_oauth repository/mcp_oauth_integration")
group(INTERNAL, "repository/scheduler_cache", "repository/scheduler_cache_unit")
group(INTERNAL, "repository/user_repo", "repository/mcp_oauth_integration")
group(INTERNAL, "repository/user_repo_email_blacklist", "repository/user_repo_email_blacklist_integration")
group(INTERNAL, "server/http", "server/request_body_read_timeout")
group(INTERNAL, "server/middleware/admin_auth", "server/middleware/custom_admin_keys")
group(INTERNAL, "server/middleware/api_key_auth", "server/middleware/api_key_auth")
group(INTERNAL, "server/middleware/api_key_auth_google", "server/middleware/api_key_auth_google")
group(INTERNAL, "server/middleware/audit_log", "server/middleware/audit_log")
group(INTERNAL, "server/middleware/cors", "server/middleware/cors")
group(INTERNAL, "server/middleware/group_model_allowlist", "server/middleware/group_model_allowlist_body_compat server/middleware/request_body_memory")
group(INTERNAL, "server/middleware/panel_rate_limit", "server/middleware/custom_admin_key_rate_limit")
group(INTERNAL, "server/middleware/request_body_limit server/middleware/request_body_memory server/middleware/request_body_read_failure",
      "server/middleware/request_body_memory")
group(INTERNAL, "server/middleware/step_up", "server/middleware/step_up")
group(INTERNAL, "server/router server/routes/mcp", "server/routes/mcp server/routes/mcp_oauth")
group(INTERNAL, "server/routes/mcp", "server/routes/mcp_rate_limit")
group(INTERNAL, "server/routes/user", "server/routes/mcp_oauth")
group(INTERNAL, "server/routes/admin", "handler/admin/email_blacklist_handler")
group(INTERNAL, "server/routes/gateway", "server/middleware/request_body_memory server/request_body_read_timeout")

group("backend/cmd/server/", "wire wire_gen", "wire_gen")
group(INTERNAL, "config/config", "config/config config/mcp_config config/openai_transport_error_cooldown")
group(INTERNAL, "managementmcp/auth managementmcp/server", "managementmcp/server")
group(INTERNAL, "managementmcp/catalog", "managementmcp/catalog")
group(INTERNAL, "middleware/rate_limiter", "middleware/rate_limiter")
group(INTERNAL, "pkg/adminaccess/catalog pkg/adminaccess/policy",
      "pkg/adminaccess/catalog_permissions pkg/adminaccess/catalog_statistics pkg/adminaccess/full_access")
group(INTERNAL, "pkg/antigravity/gemini_types", "pkg/antigravity/tool_result_images")
group(INTERNAL, "pkg/antigravity/quota_summary", "pkg/antigravity/quota_summary")
group(INTERNAL, "pkg/antigravity/request_transformer", "pkg/antigravity/request_transformer pkg/antigravity/attribution pkg/antigravity/tool_result_images")
group(INTERNAL, "pkg/antigravity/stream_transformer", "pkg/antigravity/stream_response_shape")
group(INTERNAL, "pkg/gemini/function_response", "pkg/gemini/function_response")
group(INTERNAL, "pkg/googleapi/status", "pkg/googleapi/status")
group(INTERNAL, "pkg/openai/constants", "pkg/openai/constants")
for source in "body body_budget body_read_error read_timeout upstream_drain write_timeout".split():
    group(INTERNAL, "pkg/httputil/" + source, "pkg/httputil/" + source)

# Fixture and benchmark changes are tied to their direct behavior tests.
SOURCE_TEST_FILES.update({
    "backend/internal/handler/admin/admin_service_stub_test.go": [INTERNAL + "handler/admin/email_blacklist_handler_test.go"],
    "backend/internal/repository/fixtures_integration_test.go": [INTERNAL + "repository/mcp_oauth_integration_test.go"],
    "backend/internal/service/gateway_service_benchmark_test.go": [SERVICE + "gateway_request_reuse_test.go"],
    "backend/internal/service/openai_request_patches_benchmark_test.go": [SERVICE + "openai_request_patches_test.go"],
    "backend/internal/service/openai_responses_orphan_precheck_benchmark_test.go": [SERVICE + "openai_responses_orphan_precheck_test.go"],
    "backend/internal/web/embed_on.go": [INTERNAL + "web/embed_test.go"],
    "backend/internal/pkg/openai/codex_gpt61_terra.json": [INTERNAL + "pkg/openai/constants_test.go", SERVICE + "openai_gpt61_terra_test.go"],
    "backend/migrations/249_mcp_oauth.sql": [INTERNAL + "repository/mcp_oauth_integration_test.go"],
    "backend/resources/model-pricing/model_prices_and_context_window.json": [SERVICE + "openai_images26_test.go"],
    "deploy/config.example.yaml": [INTERNAL + "config/mcp_config_test.go", INTERNAL + "config/openai_transport_error_cooldown_test.go"],
})

# These inputs only assemble already tested types/providers or version metadata.
COMPILE_ONLY_FILES = {
    "backend/cmd/server/VERSION",
    "backend/internal/handler/dto/settings.go",
    "backend/internal/handler/handler.go",
    "backend/internal/handler/wire.go",
    "backend/internal/repository/wire.go",
    "backend/internal/service/audit_log.go",
    "backend/internal/service/wire.go",
}
