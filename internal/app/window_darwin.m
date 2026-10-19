#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdatomic.h>

#include "window_darwin.h"

@interface UPSAppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate, NSPopoverDelegate,
	WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate, WKScriptMessageHandler>
@property(strong) NSWindow *window;
@property(strong) WKWebView *web;
@property(strong) NSURL *url;
@property BOOL tray; // a menu-bar icon keeps the app running without the window
@property BOOL showAtLaunch;

// The menu-bar icon: a click opens the panel, a right-click the menu.
@property(strong) NSStatusItem *statusItem;
@property(strong) NSMenu *trayMenu;
@property(strong) NSPopover *popover;
@property(strong) WKWebView *panel; // tray.html, the panel's page
@property NSUInteger panelGeneration; // cancels a pending teardown
@end

static UPSAppDelegate *appDelegate;
static atomic_bool windowOpen, panelOpen;

static const CGFloat panelWidth = 320;

@implementation UPSAppDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)note {
	if (self.tray) {
		[self makeStatusItem];
		upsappTrayStart();
	}
	if (self.showAtLaunch) {
		[self show];
	}
}

// show opens the window, or brings it forward. With the menu-bar icon, the
// app has no Dock icon while the window is closed: it gets one back first.
- (void)show {
	if (!self.window) {
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
		[self makeWindow];
	}
	[self.window makeKeyAndOrderFront:nil];
	[NSApp activateIgnoringOtherApps:YES];
}

- (void)makeWindow {
	NSRect frame = NSMakeRect(0, 0, 1440, 980);
	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
		NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
	self.window = [[NSWindow alloc] initWithContentRect:frame styleMask:style backing:NSBackingStoreBuffered defer:NO];
	self.window.title = @"PowerPilot";
	self.window.releasedWhenClosed = NO; // ARC owns it
	self.window.delegate = self;
	self.window.titlebarAppearsTransparent = YES;
	self.window.titleVisibility = NSWindowTitleHidden;
	// Match the dashboard's light page so the title bar blends into it.
	self.window.appearance = [NSAppearance appearanceNamed:NSAppearanceNameAqua];
	self.window.backgroundColor = [NSColor colorWithSRGBRed:0xf3 / 255.0 green:0xf5 / 255.0 blue:0xf8 / 255.0 alpha:1];
	// The dashboard fits the window without scrolling from 1180x700 (content) up.
	self.window.contentMinSize = NSMakeSize(1200, 720);

	self.web = [[WKWebView alloc] initWithFrame:frame configuration:[WKWebViewConfiguration new]];
	self.web.navigationDelegate = self;
	self.web.UIDelegate = self;
	[self.web setValue:@NO forKey:@"drawsBackground"];
	self.window.contentView = self.web;

	[self.window center];
	[self.window setFrameAutosaveName:@"PowerPilotMain"];
	[self load];
	atomic_store(&windowOpen, true);
}

// Closing the window ends the dashboard rather than leaving it running
// hidden. With the menu-bar icon the app stays, without a Dock icon;
// otherwise it quits.
- (void)windowWillClose:(NSNotification *)note {
	atomic_store(&windowOpen, false);
	if (!self.tray) {
		return;
	}
	[self.window saveFrameUsingName:@"PowerPilotMain"];
	[self.window setFrameAutosaveName:@""]; // free for the next window
	NSWindow *closing = self.window;
	WKWebView *web = self.web;
	self.window = nil;
	self.web = nil;
	// Let go of them once AppKit is done closing, unless the window was
	// opened again meanwhile.
	dispatch_async(dispatch_get_main_queue(), ^{
		(void)closing, (void)web;
		if (!self.window) {
			[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		}
	});
}

// Opening the app again (Finder, the Dock, Spotlight) shows the window. At
// login the menu-bar icon's LaunchAgent starts the app, and macOS sends these
// here rather than starting a second one.
- (BOOL)applicationShouldHandleReopen:(NSApplication *)app hasVisibleWindows:(BOOL)visible {
	[self show];
	return NO;
}

- (void)load {
	[self.web loadRequest:[NSURLRequest requestWithURL:self.url]];
}

// ---------- the menu-bar icon ----------

- (void)makeStatusItem {
	self.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
	NSStatusBarButton *button = self.statusItem.button;
	button.target = self;
	button.action = @selector(statusItemClicked:);
	[button sendActionOn:NSEventMaskLeftMouseUp | NSEventMaskRightMouseUp];
	button.toolTip = @"PowerPilot";

	self.trayMenu = [NSMenu new];
	self.trayMenu.delegate = self;
	[self addTrayItem:@"Open Dashboard" symbol:@"macwindow" action:@selector(openDashboard:)];
	[self addTrayItem:@"Show Summary" symbol:@"bell.badge" action:@selector(postSummary:)];
	[self.trayMenu addItem:[NSMenuItem separatorItem]];
	[self addTrayItem:@"Quit PowerPilot" symbol:@"power" action:@selector(terminate:)].target = NSApp;
}

- (NSMenuItem *)addTrayItem:(NSString *)title symbol:(NSString *)symbol action:(SEL)action {
	NSMenuItem *item = [self.trayMenu addItemWithTitle:title action:action keyEquivalent:@""];
	item.target = self;
	item.image = [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:nil];
	return item;
}

- (void)setTrayIcon:(NSData *)png title:(NSString *)title tooltip:(NSString *)tooltip {
	NSStatusBarButton *button = self.statusItem.button;
	NSImage *image = [[NSImage alloc] initWithData:png];
	image.size = NSMakeSize(18, 18);
	image.template = YES;
	button.image = image;
	// Digits that don't shift as the charge changes.
	NSFont *font = [NSFont monospacedDigitSystemFontOfSize:12 weight:NSFontWeightMedium];
	button.attributedTitle = [[NSAttributedString alloc] initWithString:title.length ? [@" " stringByAppendingString:title] : @""
		attributes:@{NSFontAttributeName: font}];
	button.imagePosition = title.length ? NSImageLeading : NSImageOnly;
	button.toolTip = tooltip;
}

- (void)statusItemClicked:(NSStatusBarButton *)button {
	NSEvent *event = NSApp.currentEvent;
	if (event.type == NSEventTypeRightMouseUp || (event.modifierFlags & NSEventModifierFlagControl)) {
		[self.popover performClose:nil];
		// Attached only for this click, so a plain click reaches here again.
		self.statusItem.menu = self.trayMenu;
		[button performClick:nil];
		return;
	}
	[self togglePanel];
}

- (void)menuDidClose:(NSMenu *)menu {
	if (menu == self.trayMenu) {
		dispatch_async(dispatch_get_main_queue(), ^{
			self.statusItem.menu = nil;
		});
	}
}

- (void)openDashboard:(id)sender {
	[self.popover performClose:nil];
	[self show];
}

- (void)postSummary:(id)sender {
	upsappTraySummary();
}

// ---------- the panel ----------

- (void)togglePanel {
	if (self.popover.shown) {
		[self.popover performClose:nil];
		return;
	}
	self.panelGeneration++;
	if (!self.popover) {
		[self makePanel];
	}
	// Active, so that a click anywhere else closes the panel.
	[NSApp activateIgnoringOtherApps:YES];
	NSStatusBarButton *button = self.statusItem.button;
	[self.popover showRelativeToRect:button.bounds ofView:button preferredEdge:NSRectEdgeMinY];
	atomic_store(&panelOpen, true);
}

- (void)makePanel {
	WKWebViewConfiguration *config = [WKWebViewConfiguration new];
	[config.userContentController addScriptMessageHandler:self name:@"powerpilot"];
	self.panel = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, panelWidth, 430) configuration:config];
	self.panel.navigationDelegate = self;
	self.panel.UIDelegate = self;
	[self.panel setValue:@NO forKey:@"drawsBackground"]; // the popover's material shows through

	NSViewController *content = [NSViewController new];
	content.view = self.panel;
	self.popover = [NSPopover new];
	self.popover.contentViewController = content;
	self.popover.contentSize = NSMakeSize(panelWidth, 430);
	self.popover.behavior = NSPopoverBehaviorTransient;
	self.popover.delegate = self;
	[self loadPanel];
}

- (void)loadPanel {
	[self.panel loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:@"/tray.html" relativeToURL:self.url]]];
}

// A closed panel's page keeps following the service for a minute, for a
// quick look again, then goes.
- (void)popoverDidClose:(NSNotification *)note {
	atomic_store(&panelOpen, false);
	NSUInteger generation = ++self.panelGeneration;
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
		if (generation == self.panelGeneration && !self.popover.shown) {
			[self.panel.configuration.userContentController removeScriptMessageHandlerForName:@"powerpilot"];
			self.popover = nil;
			self.panel = nil;
		}
	});
}

// The panel's page asks for these: its height, the dashboard, a summary
// notification, or to quit.
- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
	if (message.webView != self.panel || ![message.body isKindOfClass:[NSDictionary class]]) {
		return;
	}
	NSDictionary *body = message.body;
	NSString *action = body[@"action"];
	if ([action isEqualToString:@"size"]) {
		CGFloat height = [body[@"height"] doubleValue];
		if (height > 0) {
			self.popover.contentSize = NSMakeSize(panelWidth, MIN(height, 800));
		}
	} else if ([action isEqualToString:@"open"]) {
		[self openDashboard:nil];
	} else if ([action isEqualToString:@"summary"]) {
		upsappTraySummary();
	} else if ([action isEqualToString:@"quit"]) {
		[NSApp terminate:nil];
	}
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)app {
	return !self.tray;
}

// Links that open a new window (release notes) go to the default browser.
- (WKWebView *)webView:(WKWebView *)web createWebViewWithConfiguration:(WKWebViewConfiguration *)config
	forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
	NSURL *url = action.request.URL;
	if ([url.scheme isEqualToString:@"https"]) {
		[[NSWorkspace sharedWorkspace] openURL:url];
	}
	return nil;
}

// Log exports are downloads: links with a download attribute, or replies sent
// as attachments. WKWebView drops them unless told to download.
- (void)webView:(WKWebView *)web decidePolicyForNavigationAction:(WKNavigationAction *)action
	decisionHandler:(void (^)(WKNavigationActionPolicy))decide {
	// The panel, which can ask the app for things, stays on the service's pages.
	NSURL *url = action.request.URL;
	BOOL samePort = url.port == self.url.port || [url.port isEqual:self.url.port];
	if (web == self.panel && ![url.scheme isEqualToString:@"about"] && !([url.host isEqualToString:self.url.host] && samePort)) {
		decide(WKNavigationActionPolicyCancel);
		return;
	}
	decide(action.shouldPerformDownload ? WKNavigationActionPolicyDownload : WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)web decidePolicyForNavigationResponse:(WKNavigationResponse *)response
	decisionHandler:(void (^)(WKNavigationResponsePolicy))decide {
	NSHTTPURLResponse *http = [response.response isKindOfClass:[NSHTTPURLResponse class]] ? (NSHTTPURLResponse *)response.response : nil;
	BOOL attachment = [[http valueForHTTPHeaderField:@"Content-Disposition"] hasPrefix:@"attachment"];
	decide(attachment || !response.canShowMIMEType ? WKNavigationResponsePolicyDownload : WKNavigationResponsePolicyAllow);
}

- (void)webView:(WKWebView *)web navigationAction:(WKNavigationAction *)action didBecomeDownload:(WKDownload *)download {
	download.delegate = self;
}

- (void)webView:(WKWebView *)web navigationResponse:(WKNavigationResponse *)response didBecomeDownload:(WKDownload *)download {
	download.delegate = self;
}

// Ask where to save, starting with the name the dashboard suggested.
- (void)download:(WKDownload *)download decideDestinationUsingResponse:(NSURLResponse *)response
	suggestedFilename:(NSString *)name completionHandler:(void (^)(NSURL *))done {
	NSSavePanel *panel = [NSSavePanel savePanel];
	panel.nameFieldStringValue = name;
	[panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result) {
		done(result == NSModalResponseOK ? panel.URL : nil);
	}];
}

// The service may still be starting (or not installed): explain, then keep retrying.
- (void)webView:(WKWebView *)web didFailProvisionalNavigation:(WKNavigation *)nav withError:(NSError *)err {
	// A navigation that turned into a download "fails" this way; the page is fine.
	if ([err.domain isEqualToString:@"WebKitErrorDomain"] && err.code == 102) {
		return;
	}
	if (web == self.panel) {
		NSString *html = @"<html><head><meta name=\"color-scheme\" content=\"light dark\"></head>"
			"<body style=\"font:13px -apple-system;margin:0;padding:22px;text-align:center;background:transparent\">"
			"<b style=\"font-size:14px\">PowerPilot service not running</b>"
			"<p style=\"opacity:.65;margin:6px 0 0\">It starts at login. Retrying…</p></body></html>";
		[web loadHTMLString:html baseURL:nil];
		self.popover.contentSize = NSMakeSize(panelWidth, 96);
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
			if (self.panel == web) {
				[self loadPanel];
			}
		});
		return;
	}
	NSString *html = @"<html><body style=\"font:15px -apple-system;display:flex;align-items:center;"
		"justify-content:center;height:100vh;margin:0;color:#898781\"><div style=\"text-align:center\">"
		"<h2 style=\"font-weight:600\">Starting PowerPilot…</h2>"
		"<p>If this persists, run <code>powerpilot install</code>.</p></div></body></html>";
	[web loadHTMLString:html baseURL:nil];
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
		[self load];
	});
}

@end

static NSMenuItem *submenu(NSMenu *bar, NSString *title) {
	NSMenuItem *item = [NSMenuItem new];
	item.submenu = [[NSMenu alloc] initWithTitle:title];
	[bar addItem:item];
	return item;
}

void upsapp_run(const char *url, bool tray, bool show) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		// Only the menu-bar icon until the window opens: no Dock icon.
		[app setActivationPolicy:show || !tray ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];

		NSMenu *bar = [NSMenu new];
		NSMenu *appMenu = submenu(bar, @"PowerPilot").submenu;
		[appMenu addItemWithTitle:@"Reload" action:@selector(reload:) keyEquivalent:@"r"];
		[appMenu addItem:[NSMenuItem separatorItem]];
		[appMenu addItemWithTitle:@"Quit PowerPilot" action:@selector(terminate:) keyEquivalent:@"q"];
		NSMenu *edit = submenu(bar, @"Edit").submenu;
		[edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
		[edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
		NSMenu *window = submenu(bar, @"Window").submenu;
		[window addItemWithTitle:@"Minimize" action:@selector(performMiniaturize:) keyEquivalent:@"m"];
		[window addItemWithTitle:@"Close" action:@selector(performClose:) keyEquivalent:@"w"];
		app.mainMenu = bar;

		appDelegate = [UPSAppDelegate new];
		appDelegate.url = [NSURL URLWithString:[NSString stringWithUTF8String:url]];
		appDelegate.tray = tray;
		appDelegate.showAtLaunch = show;
		app.delegate = appDelegate;
		[app run];
	}
}

void upsapp_show(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[appDelegate show];
	});
}

void upsapp_quit(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp terminate:nil];
	});
}

bool upsapp_in_use(void) {
	return atomic_load(&windowOpen) || atomic_load(&panelOpen);
}

void upsapp_set_tray(const void *png, int len, const char *title, const char *tooltip) {
	NSData *icon = [NSData dataWithBytes:png length:len];
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *tip = [NSString stringWithUTF8String:tooltip];
	dispatch_async(dispatch_get_main_queue(), ^{
		[appDelegate setTrayIcon:icon title:t tooltip:tip];
	});
}
