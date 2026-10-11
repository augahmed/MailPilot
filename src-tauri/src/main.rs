#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod bridge;

use bridge::{Bridge, Health};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use tauri::Manager;

type Backend = Arc<Mutex<Result<Bridge, String>>>;

struct SmokeTest {
    enabled: bool,
    completed: AtomicBool,
}

#[tauri::command]
async fn health(
    app: tauri::AppHandle,
    backend: tauri::State<'_, Backend>,
) -> Result<Health, String> {
    let backend = backend.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut guard = backend.try_lock().map_err(|_| "Backend is busy")?;
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
            app.exit(0);
        });
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
            app.manage(SmokeTest { enabled: smoke_test, completed: AtomicBool::new(false) });
            if smoke_test {
                let handle = app.handle().clone();
                std::thread::spawn(move || {
                    std::thread::sleep(std::time::Duration::from_secs(20));
                    if !handle.state::<SmokeTest>().completed.load(Ordering::SeqCst) {
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
        .invoke_handler(tauri::generate_handler![health])
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
