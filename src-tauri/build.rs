fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "health",
            "local_index",
            "vector_search",
            "category_vectors",
            "gmail_status",
            "gmail_connect",
            "gmail_check",
            "gmail_disconnect",
            "sync_start",
            "sync_status",
            "sync_cancel",
            "mailbox_list",
            "mailbox_read",
            "categories_manage",
            "classification_review",
            "organization_manage",
            "reply_manage",
            "ai_settings",
            "email_summarize",
            "email_answer",
        ]),
    ))
    .expect("failed to build Tauri application manifest");
}
