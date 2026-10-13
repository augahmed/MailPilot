fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "health",
            "gmail_status",
            "gmail_connect",
            "gmail_check",
            "gmail_disconnect",
            "sync_start",
            "sync_status",
            "mailbox_list",
            "categories_manage",
            "classification_review",
            "organization_manage",
            "semantic_search",
            "reply_manage",
        ]),
    ))
    .expect("failed to build Tauri application manifest");
}
