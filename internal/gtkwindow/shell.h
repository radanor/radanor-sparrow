/* GTK3 + WebKitGTK glue for the Radanor Sparrow window helper.
 *
 * This is the product's only C code. It lives in the isolated helper binary
 * (radanor-sparrow-window) that the supervisor starts, never inside the
 * monitoring core: the core stays CGO-free, and a machine without GTK or
 * WebKitGTK still runs the product with the authenticated browser fallback.
 * docs/rebranding/LINUX_WINDOW_PORTABILITY.md records why the window cannot be
 * a hard-linked part of the main binary.
 *
 * The decisions that matter for security are not made here. Navigation policy,
 * readiness and failure are decided in Go and reported through the exported
 * radanorGo* functions below, so the rule has one implementation that the tests
 * read directly.
 *
 * Everything here is static on purpose: this header is included by more than one
 * translation unit of the cgo build (the package's own and the generated
 * export file), and static definitions cannot collide the way global ones would.
 */
#ifndef RADANOR_SPARROW_WINDOW_SHELL_H
#define RADANOR_SPARROW_WINDOW_SHELL_H

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

/* Implemented in Go with //export. WebKit and GTK emit their signals on the
 * GTK main thread, which cgo has entered, so these are ordinary calls. The
 * parameter types are declared exactly as cgo generates them, so the two
 * translation units agree. */
extern int radanorGoDecideNavigation(char *uri);
extern void radanorGoLoaded(void);
extern void radanorGoLoadFailed(char *reason);
extern void radanorGoWindowClosed(void);

/* The window's program name. GDK derives the X11 WM_CLASS and the Wayland
 * xdg_toplevel app_id from it while GTK initializes, and desktop shells match a
 * running window to its launcher entry by exactly that value, so it is set
 * explicitly instead of being taken from whatever this file happens to be called.
 * internal/desktop pins it against the installed Starter entry. */
#define RADANOR_PROGRAM_NAME "radanor-sparrow"

typedef struct {
    GtkWidget *window;
    GtkWidget *view;
    WebKitWebContext *context;
    WebKitWebsiteDataManager *manager;
    GdkPixbuf *icon;
    char *error;
} radanor_window;

static radanor_window *radanor_window_current = NULL;

static void radanor_window_set_error(const char *text) {
    if (radanor_window_current == NULL) {
        return;
    }
    g_free(radanor_window_current->error);
    radanor_window_current->error = g_strdup(text);
}

/* The approved artwork, decoded from the bytes the Go side embeds. A failure
 * here only costs the window icon; it is never fatal. */
static GdkPixbuf *radanor_window_pixbuf(const guint8 *data, gsize length) {
    GdkPixbufLoader *loader = gdk_pixbuf_loader_new();
    GError *error = NULL;
    GdkPixbuf *pixbuf = NULL;
    if (gdk_pixbuf_loader_write(loader, data, length, &error) &&
        gdk_pixbuf_loader_close(loader, &error)) {
        pixbuf = gdk_pixbuf_loader_get_pixbuf(loader);
        if (pixbuf != NULL) {
            g_object_ref(pixbuf);
        }
    }
    if (error != NULL) {
        g_error_free(error);
    }
    g_object_unref(loader);
    return pixbuf;
}

/* --- signal handlers --- */

/* X is a request, not a decision: Go decides whether the window hides or the
 * helper exits, so the lifecycle rule stays in one language. */
static gboolean radanor_window_on_delete(GtkWidget *widget, GdkEvent *event, gpointer data) {
    (void)widget;
    (void)event;
    (void)data;
    radanorGoWindowClosed();
    return TRUE;
}

/* Only the authenticated loopback dashboard may be loaded. Everything else is
 * ignored here rather than handed to the desktop's default handler, and a
 * requested new window is refused outright. */
static gboolean radanor_window_on_decide(WebKitWebView *view, WebKitPolicyDecision *decision,
                                         WebKitPolicyDecisionType type, gpointer data) {
    (void)view;
    (void)data;
    if (type == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
        webkit_policy_decision_ignore(decision);
        return TRUE;
    }
    if (type != WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
        return FALSE;
    }
    WebKitNavigationAction *action =
        webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(decision));
    WebKitURIRequest *request = webkit_navigation_action_get_request(action);
    const char *uri = request == NULL ? NULL : webkit_uri_request_get_uri(request);
    if (uri != NULL && radanorGoDecideNavigation((char *)uri)) {
        return FALSE;
    }
    webkit_policy_decision_ignore(decision);
    return TRUE;
}

/* No second WebKit view is ever created: window.open, target=_blank and
 * middle-click are all refused. */
static GtkWidget *radanor_window_on_create(WebKitWebView *view, WebKitNavigationAction *action, gpointer data) {
    (void)view;
    (void)action;
    (void)data;
    return NULL;
}

static void radanor_window_on_load_changed(WebKitWebView *view, WebKitLoadEvent event, gpointer data) {
    (void)view;
    (void)data;
    if (event == WEBKIT_LOAD_FINISHED) {
        radanorGoLoaded();
    }
}

static gboolean radanor_window_on_load_failed(WebKitWebView *view, WebKitLoadEvent event,
                                              const gchar *failing_uri, GError *error, gpointer data) {
    (void)event;
    (void)data;
    const gchar *current = webkit_web_view_get_uri(view);
    /* A failing subresource must not tear the window down; only a failure of
     * the dashboard document itself is reported. */
    if (current != NULL && failing_uri != NULL && g_strcmp0(current, failing_uri) == 0) {
        radanorGoLoadFailed((char *)(error != NULL ? error->message : "the dashboard could not be loaded"));
        return TRUE;
    }
    return FALSE;
}

/* --- API used by Go --- */

static int radanor_window_available(void) {
    /* Set before GTK initializes: this is the value GDK reads for the window's
     * class and application id. */
    g_set_prgname(RADANOR_PROGRAM_NAME);
    /* gtk_init_check reports a missing or unusable display instead of aborting
     * the process, which is what makes the browser fallback possible. */
    return gtk_init_check(NULL, NULL) ? 1 : 0;
}

static int radanor_window_create(const char *title, const char *url, const guint8 *icon, gsize icon_length,
                                 const char *data_dir, int width, int height) {
    if (radanor_window_current != NULL) {
        radanor_window_set_error("a window already exists in this process");
        return 0;
    }
    radanor_window_current = g_new0(radanor_window, 1);
    radanor_window_current->window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(radanor_window_current->window), title);
    gtk_window_set_default_size(GTK_WINDOW(radanor_window_current->window), width, height);
    gtk_window_set_position(GTK_WINDOW(radanor_window_current->window), GTK_WIN_POS_CENTER);
    if (icon != NULL && icon_length > 0) {
        radanor_window_current->icon = radanor_window_pixbuf(icon, icon_length);
        if (radanor_window_current->icon != NULL) {
            gtk_window_set_icon(GTK_WINDOW(radanor_window_current->window), radanor_window_current->icon);
        }
    }
    /* Cookies and cache live under the private state directory, exactly as the
     * Windows window keeps its WebView2 data path there. */
    radanor_window_current->manager = webkit_website_data_manager_new(
        "base-data-directory", data_dir, "base-cache-directory", data_dir, NULL);
    radanor_window_current->context =
        webkit_web_context_new_with_website_data_manager(radanor_window_current->manager);
    /* Sandbox every WebKit subprocess, from the first one. WebKitGTK's
     * subprocess sandbox is opt-in, and this call is only valid before any web
     * process exists: the context is still unused here and the view that spawns
     * one is created on the next line. Enabling it never relaxes a system
     * control - it is additional bubblewrap and seccomp confinement around the
     * processes that parse the dashboard.
     *
     * Requesting it is not a claim that it worked. The window cannot start
     * without it: on a host where the sandbox cannot run, the web process fails
     * and the dashboard does not load, so the component reports that reason and
     * the supervisor opens the authenticated browser instead of presenting an
     * unsandboxed window. The state is measured from /proc by
     * internal/gtkwindow's native tests and recorded in
     * docs/rebranding/LINUX_WINDOW_SECURITY_L3.md. */
    webkit_web_context_set_sandbox_enabled(radanor_window_current->context, TRUE);
    radanor_window_current->view = webkit_web_view_new_with_context(radanor_window_current->context);
    gtk_container_add(GTK_CONTAINER(radanor_window_current->window), radanor_window_current->view);

    g_signal_connect(radanor_window_current->window, "delete-event",
                     G_CALLBACK(radanor_window_on_delete), NULL);
    g_signal_connect(radanor_window_current->view, "decide-policy",
                     G_CALLBACK(radanor_window_on_decide), NULL);
    g_signal_connect(radanor_window_current->view, "create",
                     G_CALLBACK(radanor_window_on_create), NULL);
    g_signal_connect(radanor_window_current->view, "load-changed",
                     G_CALLBACK(radanor_window_on_load_changed), NULL);
    g_signal_connect(radanor_window_current->view, "load-failed",
                     G_CALLBACK(radanor_window_on_load_failed), NULL);

    gtk_widget_show_all(radanor_window_current->window);
    gtk_window_present(GTK_WINDOW(radanor_window_current->window));
    webkit_web_view_load_uri(WEBKIT_WEB_VIEW(radanor_window_current->view), url);
    return 1;
}

static void radanor_window_run(void) {
    gtk_main();
    /* Release what this process created once the loop has ended. The helper
     * shows one window and exits, so this is tidiness there; it also keeps the
     * package's native tests honest, because a second Run in the same process
     * sees a clean slate rather than a stale global. */
    if (radanor_window_current != NULL) {
        if (radanor_window_current->window != NULL) {
            gtk_widget_destroy(radanor_window_current->window);
        }
        if (radanor_window_current->icon != NULL) {
            g_object_unref(radanor_window_current->icon);
        }
        if (radanor_window_current->context != NULL) {
            g_object_unref(radanor_window_current->context);
        }
        if (radanor_window_current->manager != NULL) {
            g_object_unref(radanor_window_current->manager);
        }
        g_free(radanor_window_current->error);
        g_free(radanor_window_current);
        radanor_window_current = NULL;
    }
}

/* --- deferred calls, so Go can reach GTK from any goroutine --- */

static gboolean radanor_window_quit_idle(gpointer data) {
    (void)data;
    gtk_main_quit();
    return G_SOURCE_REMOVE;
}

static void radanor_window_quit(void) {
    /* g_idle_add is one of the few GLib entry points that is safe from another
     * thread, and GTK itself is not. */
    g_idle_add(radanor_window_quit_idle, NULL);
}

static gboolean radanor_window_navigate_idle(gpointer data) {
    char *url = (char *)data;
    if (radanor_window_current != NULL && radanor_window_current->view != NULL) {
        webkit_web_view_load_uri(WEBKIT_WEB_VIEW(radanor_window_current->view), url);
    }
    g_free(url);
    return G_SOURCE_REMOVE;
}

static void radanor_window_navigate(const char *url) {
    g_idle_add(radanor_window_navigate_idle, g_strdup(url));
}

/* --- introspection, for the helper's own tests and for diagnostics --- */

static const char *radanor_window_title(void) {
    if (radanor_window_current == NULL || radanor_window_current->window == NULL) {
        return "";
    }
    const char *title = gtk_window_get_title(GTK_WINDOW(radanor_window_current->window));
    return title == NULL ? "" : title;
}

static const char *radanor_window_uri(void) {
    if (radanor_window_current == NULL || radanor_window_current->view == NULL) {
        return "";
    }
    const char *uri = webkit_web_view_get_uri(WEBKIT_WEB_VIEW(radanor_window_current->view));
    return uri == NULL ? "" : uri;
}

static const char *radanor_window_error(void) {
    if (radanor_window_current == NULL || radanor_window_current->error == NULL) {
        return "";
    }
    return radanor_window_current->error;
}

static int radanor_window_has_icon(void) {
    return radanor_window_current != NULL && radanor_window_current->icon != NULL ? 1 : 0;
}

#endif /* RADANOR_SPARROW_WINDOW_SHELL_H */
