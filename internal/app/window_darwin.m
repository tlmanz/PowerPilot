#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

#include "window_darwin.h"

@interface UPSAppDelegate : NSObject <NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate>
@property(strong) NSWindow *window;
@property(strong) WKWebView *web;
@property(strong) NSURL *url;
@end

@implementation UPSAppDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)note {
	NSRect frame = NSMakeRect(0, 0, 1440, 980);
	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
		NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
	self.window = [[NSWindow alloc] initWithContentRect:frame styleMask:style backing:NSBackingStoreBuffered defer:NO];
	self.window.title = @"PowerPilot";
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
	[self.window makeKeyAndOrderFront:nil];
	[self load];
	[NSApp activateIgnoringOtherApps:YES];
}

- (void)load {
	[self.web loadRequest:[NSURLRequest requestWithURL:self.url]];
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)app {
	return YES;
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

// The service may still be starting (or not installed): explain, then keep retrying.
- (void)webView:(WKWebView *)web didFailProvisionalNavigation:(WKNavigation *)nav withError:(NSError *)err {
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

void upsapp_run(const char *url) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyRegular];

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

		UPSAppDelegate *delegate = [UPSAppDelegate new];
		delegate.url = [NSURL URLWithString:[NSString stringWithUTF8String:url]];
		app.delegate = delegate;
		[app run];
	}
}
