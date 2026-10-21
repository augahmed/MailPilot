#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod ai;
mod answers;
mod bridge;
mod category_vectors;
mod indexing;
mod replies;
mod search;
mod semantic_review;

use ai::{AISettingsRequest, AISettingsResponse, SummaryRequest, SummaryResponse};
use bridge::{
    AuthStatus, Bridge, CategoryPage, CategoryRequest, ClassificationPage, ClassificationRequest,
    Health, MailboxPage, OrganizationRequest, OrganizationResponse, ReadMailRequest,
    ReadMailResponse, ReplyRequest, ReplyResponse, SyncProgress,
};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use tauri::Manager;

type Backend = Arc<Mutex<Result<Bridge, String>>>;

#[cfg(test)]
mod capability_tests {
    #[test]
    fn retired_commands_are_not_granted_to_the_main_window() {
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/main.json")).unwrap();
        let permissions = capability["permissions"].as_array().unwrap();
        for retired in ["allow-ask-manage", "allow-semantic-search"] {
            assert!(!permissions.iter().any(|p| p.as_str() == Some(retired)));
        }
        for retained in [
            "allow-mailbox-read",
            "allow-classification-review",
            "allow-organization-manage",
            "allow-reply-manage",
            "allow-ai-settings",
            "allow-email-summarize",
            "allow-email-answer",
            "allow-local-index",
            "allow-vector-search",
            "allow-category-vectors",
        ] {
            assert!(permissions.iter().any(|p| p.as_str() == Some(retained)));
        }
    }
}

struct SmokeTest {
    enabled: bool,
    completed: AtomicBool,
    auth_completed: AtomicBool,
    mailbox_completed: AtomicBool,
    reader_completed: AtomicBool,
    categories_completed: AtomicBool,
    classification_completed: AtomicBool,
    organization_completed: AtomicBool,
    reply_completed: AtomicBool,
    ai_settings_completed: AtomicBool,
    summary_completed: AtomicBool,
    index_completed: AtomicBool,
    search_completed: AtomicBool,
    answer_completed: AtomicBool,
}

impl SmokeTest {
    fn complete(&self) -> bool {
        self.completed.load(Ordering::SeqCst)
            && self.auth_completed.load(Ordering::SeqCst)
            && self.mailbox_completed.load(Ordering::SeqCst)
            && self.reader_completed.load(Ordering::SeqCst)
            && self.categories_completed.load(Ordering::SeqCst)
            && self.classification_completed.load(Ordering::SeqCst)
            && self.organization_completed.load(Ordering::SeqCst)
            && self.reply_completed.load(Ordering::SeqCst)
            && self.ai_settings_completed.load(Ordering::SeqCst)
            && self.summary_completed.load(Ordering::SeqCst)
            && self.index_completed.load(Ordering::SeqCst)
            && self.search_completed.load(Ordering::SeqCst)
            && self.answer_completed.load(Ordering::SeqCst)
    }
}

#[tauri::command]
async fn category_vectors(
    backend: tauri::State<'_, Backend>,
    request: category_vectors::CategoryVectorRequest,
) -> Result<category_vectors::CategoryVectorResponse, String> {
    let backend = backend.inner().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.category_vectors(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())?
}

#[tauri::command]
async fn email_answer(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: answers::AnswerRequest,
) -> Result<answers::AnswerResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.answer_email(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && result.evidence.is_empty()
        && result.result.is_none()
        && !smoke.answer_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_answer_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

#[tauri::command]
async fn vector_search(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: search::SearchRequest,
) -> Result<search::SearchResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.vector_search(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && result.account_id.is_empty()
        && result.emails.is_empty()
        && !smoke.search_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_search_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

#[tauri::command]
async fn local_index(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: indexing::IndexRequest,
) -> Result<indexing::IndexResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.local_index(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && result.account_id.is_empty()
        && !smoke.index_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_index_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

#[tauri::command]
async fn health(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
) -> Result<Health, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend is busy")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.health(),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.database == "connected"
        && !smoke.completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_health_ok\"}}"
        );
        std::thread::spawn(move || {
            std::thread::sleep(std::time::Duration::from_millis(750));
            if app.state::<SmokeTest>().complete() {
                app.exit(0);
            }
        });
    }
    Ok(result)
}

async fn account_command(
    backend: tauri::State<'_, Backend>,
    method: &'static str,
) -> Result<AuthStatus, String> {
    let backend = backend.inner().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) if method == "auth.begin" => bridge.auth_begin(),
            Ok(bridge) => bridge.auth_status(method),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())?
}
#[tauri::command]
async fn gmail_status(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
) -> Result<AuthStatus, String> {
    let result = account_command(backend, "auth.status").await?;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.state == "unconfigured"
        && !smoke.auth_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_auth_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

#[tauri::command]
async fn gmail_connect(backend: tauri::State<'_, Backend>) -> Result<AuthStatus, String> {
    account_command(backend, "auth.begin").await
}
#[tauri::command]
async fn gmail_check(backend: tauri::State<'_, Backend>) -> Result<AuthStatus, String> {
    account_command(backend, "auth.check").await
}
#[tauri::command]
async fn gmail_disconnect(backend: tauri::State<'_, Backend>) -> Result<AuthStatus, String> {
    account_command(backend, "auth.disconnect").await
}

#[tauri::command]
async fn categories_manage(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: CategoryRequest,
) -> Result<CategoryPage, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.categories(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code.as_deref() == Some("unauthorized")
        && result.rows.is_empty()
        && !smoke.categories_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_categories_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}
#[tauri::command]
async fn classification_review(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: ClassificationRequest,
) -> Result<ClassificationPage, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.classifications(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code.as_deref() == Some("unauthorized")
        && result.rows.is_empty()
        && !smoke.classification_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!("{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_classification_ok\"}}");
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}
#[tauri::command]
async fn organization_manage(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: OrganizationRequest,
) -> Result<OrganizationResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.organization(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && result.rows.is_empty()
        && !smoke.organization_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!("{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_organization_ok\"}}");
        if smoke.complete() {
            app.exit(0)
        }
    }
    Ok(result)
}
#[tauri::command]
async fn reply_manage(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: ReplyRequest,
) -> Result<ReplyResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.reply(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && !smoke.reply_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_reply_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

async fn sync_command(
    backend: tauri::State<'_, Backend>,
    start: bool,
) -> Result<SyncProgress, String> {
    let backend = backend.inner().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.sync(start),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())?
}
#[tauri::command]
async fn sync_cancel(backend: tauri::State<'_, Backend>) -> Result<SyncProgress, String> {
    let backend = backend.inner().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.cancel_sync(),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())?
}
#[tauri::command]
async fn sync_start(backend: tauri::State<'_, Backend>) -> Result<SyncProgress, String> {
    sync_command(backend, true).await
}
#[tauri::command]
async fn sync_status(backend: tauri::State<'_, Backend>) -> Result<SyncProgress, String> {
    sync_command(backend, false).await
}
#[tauri::command]
async fn mailbox_read(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: ReadMailRequest,
) -> Result<ReadMailResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.read_mail(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && !smoke.reader_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_reader_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}
#[tauri::command]
async fn mailbox_list(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    offset: u32,
) -> Result<MailboxPage, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.mailbox(offset),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.account_id.is_empty()
        && result.rows.is_empty()
        && !smoke.mailbox_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_mailbox_ok\"}}"
        );
        if let Some(window) = app.get_webview_window("main") {
            let _ = window.eval(r#"window.__TAURI_INTERNALS__.invoke('mailbox_read', {request:{accountId:'smoke',emailId:'smoke',offset:0,revision:''}}).catch(() => {});"#);
        }
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}

#[tauri::command]
async fn ai_settings(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: AISettingsRequest,
) -> Result<AISettingsResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.ai_settings(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && !smoke.ai_settings_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!("{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_ai_settings_ok\"}}");
        if let Some(window) = app.get_webview_window("main") {
            let _=window.eval(r#"window.__TAURI_INTERNALS__.invoke('email_summarize', {request:{operation:'preview',accountId:'smoke',emailId:'smoke',previewId:'',jobId:'',approved:false}}).catch(() => {});"#);
        }
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}
#[tauri::command]
async fn email_summarize(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: SummaryRequest,
) -> Result<SummaryResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.summarize_email(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code == "unauthorized"
        && result.email.is_empty()
        && result.result.is_none()
        && !smoke.summary_completed.swap(true, Ordering::SeqCst)
    {
        eprintln!(
            "{{\"level\":\"INFO\",\"component\":\"desktop\",\"event\":\"frontend_summary_ok\"}}"
        );
        if smoke.complete() {
            app.exit(0);
        }
    }
    Ok(result)
}
fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .setup(|app| {
            let smoke_test = std::env::args().any(|arg| arg == "--smoke-test");
            app.manage(SmokeTest { enabled: smoke_test, completed: AtomicBool::new(false), auth_completed: AtomicBool::new(false), mailbox_completed: AtomicBool::new(false), reader_completed: AtomicBool::new(false), categories_completed: AtomicBool::new(false), classification_completed: AtomicBool::new(false), organization_completed:AtomicBool::new(false), reply_completed:AtomicBool::new(false), ai_settings_completed:AtomicBool::new(false),summary_completed:AtomicBool::new(false),index_completed:AtomicBool::new(false),search_completed:AtomicBool::new(false),answer_completed:AtomicBool::new(false) });
            if smoke_test {
                let handle = app.handle().clone();
                std::thread::spawn(move || {
                    std::thread::sleep(std::time::Duration::from_secs(20));
                    if !handle.state::<SmokeTest>().complete() {
                        eprintln!("{{\"level\":\"ERROR\",\"component\":\"desktop\",\"code\":\"smoke_timeout\"}}");
                        handle.exit(1);
                    }
                });
            }
            let data_dir = app.path().app_data_dir()?;
            let backend = Bridge::start(&data_dir);
            if backend.is_err() {
                eprintln!("{{\"level\":\"ERROR\",\"component\":\"desktop\",\"code\":\"backend_start\"}}");
            }
            app.manage(Arc::new(Mutex::new(backend)) as Backend);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            email_answer,category_vectors,vector_search,local_index,health, gmail_status, gmail_connect, gmail_check, gmail_disconnect, sync_start, sync_status, sync_cancel, mailbox_list, mailbox_read, categories_manage, classification_review, organization_manage, reply_manage, ai_settings, email_summarize])
        .build(tauri::generate_context!())
        .expect("failed to build MailPilot desktop application");

    app.run(|app, event| {
        if let tauri::RunEvent::Exit = event {
            if let Some(backend) = app.try_state::<Backend>() {
                if let Ok(mut guard) = backend.lock() {
                    // Drop the child before the desktop event loop exits.
                    *guard = Err("Application stopped".into());
                }
            }
        }
    });
}
