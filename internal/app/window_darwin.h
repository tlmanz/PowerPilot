#ifndef UPSAPP_WINDOW_DARWIN_H
#define UPSAPP_WINDOW_DARWIN_H

#include <stdbool.h>

// Runs the Cocoa event loop with one window showing url. With tray, closing
// the window leaves the app in the menu bar, and show says whether the window
// opens at launch. Never returns.
void upsapp_run(const char *url, bool tray, bool show);

// Opens the window, or brings it forward. Safe from any thread.
void upsapp_show(void);

// Quits the app. Safe from any thread.
void upsapp_quit(void);

// Whether the window or the menu-bar panel is open. Safe from any thread.
bool upsapp_in_use(void);

// Shows the menu-bar icon's image (a template PNG), title and tooltip. Safe
// from any thread; it copies its arguments.
void upsapp_set_tray(const void *png, int len, const char *title, const char *tooltip);

// Go: follows the service for the menu-bar icon, once the icon is there.
void upsappTrayStart(void);

// Go: posts the latest reading as a notification.
void upsappTraySummary(void);

#endif
