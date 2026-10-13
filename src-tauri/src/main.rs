#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod bridge;
mod replies;

use bridge::{
    AuthStatus, Bridge, CategoryPage, CategoryRequest, ClassificationPage, ClassificationRequest,
    Health, MailboxPage, OrganizationRequest, OrganizationResponse, ReadMailRequest,
    ReadMailResponse, ReplyRequest, ReplyResponse, SearchRequest, SearchResponse, SyncProgress,
};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use tauri::Manager;

type Backend = Arc<Mutex<Result<Bridge, String>>>;

struct SmokeTest {
    enabled: bool,
    completed: AtomicBool,
    auth_completed: AtomicBool,
    mailbox_completed: AtomicBool,
    reader_completed: AtomicBool,
    categories_completed: AtomicBool,
    classification_completed: AtomicBool,
    organization_completed: AtomicBool,
    search_completed: AtomicBool,
    reply_completed: AtomicBool,
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
            && self.search_completed.load(Ordering::SeqCst)
            && self.reply_completed.load(Ordering::SeqCst)
    }
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
        && result.body.is_empty()
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
#[tauri::command]
async fn semantic_search(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
    request: SearchRequest,
) -> Result<SearchResponse, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.lock().map_err(|_| "Backend unavailable")?;
        match guard.as_mut() {
            Ok(bridge) => bridge.search(request),
            Err(message) => Err(message.clone()),
        }
    })
    .await
    .map_err(|_| "Backend task failed".to_string())??;
    let smoke = app.state::<SmokeTest>();
    if smoke.enabled
        && result.code.as_deref() == Some("unauthorized")
        && result.rows.is_empty()
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
            app.manage(SmokeTest { enabled: smoke_test, completed: AtomicBool::new(false), auth_completed: AtomicBool::new(false), mailbox_completed: AtomicBool::new(false), reader_completed: AtomicBool::new(false), categories_completed: AtomicBool::new(false), classification_completed: AtomicBool::new(false), organization_completed:AtomicBool::new(false), search_completed:AtomicBool::new(false), reply_completed:AtomicBool::new(false) });
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
        .invoke_handler(tauri::generate_handler![health, gmail_status, gmail_connect, gmail_check, gmail_disconnect, sync_start, sync_status, sync_cancel, mailbox_list, mailbox_read, categories_manage, classification_review, organization_manage, semantic_search, reply_manage])
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
