package backend

import "encoding/json/jsontext"

// The commands of the subsystems this build does not have yet, and why
// each waits:
//
//   - Textus AI's agent and what hangs off it: the chat turn and its tools,
//     attachments, generated images, memory, skills and the agent
//     workspace (agent.md, the skill layers), project files, the paper
//     writer, and the records of the files a turn changed. They come
//     together with the agent, which they serve.
//   - The grammar check: it runs the rules engine beside the model, as the
//     inline completion's last pass does (ai_complete works without it).
//   - ChatGPT and Claude sign-in: those providers are reached by API key.
//   - Plugins and what they reach (files, git, ssh, http, stores, MCP,
//     agents). Scheduled jobs, notifications and floating windows are
//     plugin features: their grants are the plugins', so they come with
//     plugins.
//   - Chat windows, which hold the agent's conversation; web page tabs.
//   - The command-line tool, whose install needs the CLI mode itself, and
//     the default editor.
//   - The iOS build's own files.
//
// A command whose answer only reports what is there answers that there is
// nothing (no plugin, no key, no job), so the pages that ask as they open
// carry on; one that would do something fails with errUnsupported, which
// the page shows as that action failing.

// unsupportedCommands fail with errUnsupported.
var unsupportedCommands = []string{
	// Textus AI's agent, sign-in and the grammar check
	"ai_agent_message", "ai_steer", "pick_attachment_files", "attach_pasted",
	"ai_generated_image", "ai_retry_generated_image", "ai_generated_image_prompt", "ai_redraw_generated_image",
	"memory_pass", "ai_offer_skill", "library_use", "memory_undo", "memory_move", "memory_restore",
	"save_offered_skill", "memory_delete", "memory_remember",
	"ai_paper_run", "ai_paper_save_outline", "ai_paper_draft", "ai_paper_status",
	"ai_file_changes", "ai_set_file_changes", "ensure_project_file", "import_references_picked", "learn_style",
	"list_project_files", "pick_text_files", "read_project_file", "ensure_global_agent_md", "load_agent_workspace",
	"open_global_agent_dir", "import_agent_skill", "customize_builtin_skill", "reset_builtin_skill",
	"set_skill_modes", "set_skill_off", "save_written_skill", "delete_global_skill",
	"ai_grammar_check",
	"codex_login", "codex_logout", "claude_login", "claude_logout",
	// chat and floating windows, web pages
	"start_floating_tab_drag", "open_floating_window", "resize_floating_window", "floating_window_position",
	"show_floating_owner", "detach_chat_window",
	"web_page_show", "web_page_navigate", "web_page_reload",
	// the command-line tool, the default editor
	"install_cli_command", "set_default_editor",
	// plugins and what they reach
	"plugin_fs_list", "plugin_fs_documents", "plugin_fs_search", "plugin_fs_write", "plugin_fs_rename",
	"plugin_fs_trash", "plugin_fs_trash_path", "plugin_fs_mkdir", "plugin_fs_stat", "plugin_fs_create",
	"plugin_fs_read_versioned", "plugin_app_settings", "plugin_fs_write_if", "plugin_secret_get", "plugin_secret_set",
	"plugin_metadata_all", "plugin_metadata_get", "plugin_metadata_of_texts",
	"plugin_git_status", "plugin_git_log", "plugin_git_show", "plugin_git_commit", "plugin_git_fetch",
	"plugin_git_push", "plugin_git_pull", "git_trust_repository",
	"plugin_ssh_hosts", "plugin_ssh_allow", "plugin_ssh_connect", "plugin_ssh_home", "plugin_ssh_list",
	"plugin_ssh_write", "plugin_ssh_create", "plugin_ssh_rename", "plugin_ssh_remove", "plugin_ssh_close",
	"plugin_http", "plugin_store_get", "plugin_store_set", "plugin_store_keys", "plugin_store_read",
	"plugin_store_write", "plugin_store_files", "plugin_store_delete", "plugin_store_usage", "plugin_store_clear",
	"plugin_export", "plugins_set_all", "plugin_ai_status", "plugin_ai_allow", "plugin_ai_complete",
	"plugin_ai_stream", "plugin_set", "plugin_install", "plugin_install_files", "plugin_load_dev",
	"plugin_uninstall", "plugin_pick_package", "plugin_open_scripts_dir", "plugin_catalog", "plugin_catalog_readme",
	"plugin_catalog_icon", "skill_catalog", "skill_install_catalog", "plugin_install_catalog",
	"mcp_restart", "mcp_try", "agent_start", "agent_send",
	"plugin_file", "plugin_fs_read", "plugin_read_resource", "plugin_ssh_read",
	// scheduled jobs and notifications
	"schedule_set", "notify_show",
	// the iOS build's own files (native-files.ts)
	"appearance", "begin_access", "create_document", "delete_entry", "haptic", "list_folder", "pick_document",
	"pick_folder", "read_data", "read_text", "remembered", "rename_entry", "share", "trash", "write_text",
}

// emptyAnswers are the commands that report what is there, with this
// build's answer: nothing.
var emptyAnswers = map[string]string{
	// nothing to report
	"plugins_state":               `{"allOn":false,"plugins":{}}`,
	"plugins_installed":           `[]`,
	"plugin_take_uris":            `[]`,
	"take_pending_plugin_command": `null`,
	"plugin_catalog_cached":       `null`,
	"plugin_dev_stamp":            `null`,
	"mcp_status":                  `[]`,
	"schedule_list":               `[]`,
	"schedule_take_runs":          `[]`,
	"schedule_clear":              `false`,
	"notify_take_open":            `null`,
	"claude_auth_status":          `{"configured":false}`,
	"codex_auth_status":           `{"configured":false}`,
	"ai_can_think_deeper":         `false`,
	"web_search_status":           `{"native":false,"service":null}`,
	"list_skills":                 `[]`,
	"list_image_providers":        `[]`,
	"current_chat_window":         `null`,
	"take_chat_handoff":           `null`,
	"floating_desktop_supported":  `false`,
	"list_window_bounds":          `[]`,
	"start_window_drag_tracking":  `false`,
	"cli_command_status":          `"missing"`,
	// told, and nothing to do with it
	"set_plugin_menu":            `null`,
	"set_plugin_exports":         `null`,
	"set_tray_plugin_items":      `null`,
	"plugin_ai_tools_set":        `null`,
	"plugin_ai_tool_result":      `null`,
	"plugin_http_cancel":         `null`,
	"agent_stop":                 `null`,
	"ai_cancel":                  `null`,
	"ai_discard_generated_image": `null`,
	"ai_paper_figures_drawn":     `null`,
	"background_host_idle":       `null`,
	"cli_listening":              `null`,
	"cli_respond":                `null`,
	"close_floating_window":      `null`,
	"web_page_hide":              `null`,
	"web_page_close":             `null`,
}

func init() {
	for _, name := range unsupportedCommands {
		command(name, func(c *Call, _ none) (nothing, error) { return nil, errUnsupported(c.name) })
	}
	for name, answer := range emptyAnswers {
		value := jsontext.Value(answer)
		command(name, func(c *Call, _ none) (jsontext.Value, error) { return value, nil })
	}
}
