#include "hid_darwin.h"

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/hid/IOHIDManager.h>
#include <stdlib.h>
#include <string.h>

// The UPS's USB-serial bridge only accepts fixed 8-byte output reports.
#define REPORT_SIZE 8

struct upshid_dev {
	IOHIDManagerRef mgr;
	IOHIDDeviceRef dev;
	int mgr_open, dev_open, scheduled;
	volatile int removed;
	uint8_t report[64];
	char rx[512];
	int rxlen;
};

static void on_report(void *ctx, IOReturn result, void *sender, IOHIDReportType type,
		uint32_t id, uint8_t *report, CFIndex len) {
	upshid_dev *d = ctx;
	for (CFIndex i = 0; i < len && d->rxlen < (int)sizeof d->rx; i++)
		d->rx[d->rxlen++] = (char)report[i];
}

static void on_removed(void *ctx, IOReturn result, void *sender) {
	((upshid_dev *)ctx)->removed = 1;
}

static CFMutableDictionaryRef match_dict(int vid, int pid) {
	CFMutableDictionaryRef m = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFNumberRef v = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &vid);
	CFNumberRef p = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &pid);
	CFDictionarySetValue(m, CFSTR(kIOHIDVendorIDKey), v);
	CFDictionarySetValue(m, CFSTR(kIOHIDProductIDKey), p);
	CFRelease(v);
	CFRelease(p);
	return m;
}

upshid_dev *upshid_open(int vid, int pid, int *status) {
	upshid_dev *d = calloc(1, sizeof *d);
	if (!d) {
		*status = kIOReturnNoMemory;
		return NULL;
	}

	d->mgr = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
	CFMutableDictionaryRef match = match_dict(vid, pid);
	IOHIDManagerSetDeviceMatching(d->mgr, match);
	CFRelease(match);

	IOReturn rc = IOHIDManagerOpen(d->mgr, kIOHIDOptionsTypeNone);
	if (rc != kIOReturnSuccess) {
		*status = rc;
		goto fail;
	}
	d->mgr_open = 1;

	CFSetRef devices = IOHIDManagerCopyDevices(d->mgr);
	CFIndex n = devices ? CFSetGetCount(devices) : 0;
	if (n == 0) {
		if (devices)
			CFRelease(devices);
		*status = UPSHID_ERR_NOT_FOUND;
		goto fail;
	}
	const void **list = calloc(n, sizeof *list);
	CFSetGetValues(devices, list);
	d->dev = (IOHIDDeviceRef)CFRetain(list[0]);
	free(list);
	CFRelease(devices);

	rc = IOHIDDeviceOpen(d->dev, kIOHIDOptionsTypeNone);
	if (rc != kIOReturnSuccess) {
		*status = rc;
		goto fail;
	}
	d->dev_open = 1;

	IOHIDDeviceRegisterInputReportCallback(d->dev, d->report, sizeof d->report, on_report, d);
	IOHIDDeviceRegisterRemovalCallback(d->dev, on_removed, d);
	IOHIDDeviceScheduleWithRunLoop(d->dev, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
	d->scheduled = 1;

	*status = UPSHID_OK;
	return d;

fail:
	upshid_close(d);
	return NULL;
}

// upshid_exchange drops stale input, writes cmd as zero-padded output reports,
// then gathers input reports until a '\r' arrives or the timeout passes.
int upshid_exchange(upshid_dev *d, const char *cmd, int cmdlen, char *out, int outcap, int *outlen, double timeout) {
	*outlen = 0;
	if (d->removed)
		return UPSHID_ERR_REMOVED;

	for (int i = 0; i < 64 && CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0, true) == kCFRunLoopRunHandledSource; i++)
		;
	d->rxlen = 0;

	for (int off = 0; off < cmdlen; off += REPORT_SIZE) {
		uint8_t chunk[REPORT_SIZE] = {0};
		int n = cmdlen - off < REPORT_SIZE ? cmdlen - off : REPORT_SIZE;
		memcpy(chunk, cmd + off, n);
		IOReturn rc = IOHIDDeviceSetReport(d->dev, kIOHIDReportTypeOutput, 0, chunk, REPORT_SIZE);
		if (rc != kIOReturnSuccess)
			return d->removed ? UPSHID_ERR_REMOVED : rc;
	}

	CFAbsoluteTime deadline = CFAbsoluteTimeGetCurrent() + timeout;
	char *cr;
	while (!(cr = memchr(d->rx, '\r', d->rxlen))) {
		if (d->removed)
			return UPSHID_ERR_REMOVED;
		CFTimeInterval left = deadline - CFAbsoluteTimeGetCurrent();
		if (left <= 0)
			break;
		CFRunLoopRunInMode(kCFRunLoopDefaultMode, left < 0.05 ? left : 0.05, true);
	}

	int end = cr ? (int)(cr - d->rx) : d->rxlen;
	for (int i = 0; i < end && *outlen < outcap; i++)
		if (d->rx[i] != '\0')
			out[(*outlen)++] = d->rx[i];
	return cr ? UPSHID_OK : UPSHID_ERR_TIMEOUT;
}

void upshid_close(upshid_dev *d) {
	if (!d)
		return;
	if (d->dev) {
		if (d->scheduled) {
			IOHIDDeviceRegisterInputReportCallback(d->dev, d->report, sizeof d->report, NULL, NULL);
			IOHIDDeviceRegisterRemovalCallback(d->dev, NULL, NULL);
			IOHIDDeviceUnscheduleFromRunLoop(d->dev, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
		}
		if (d->dev_open && !d->removed)
			IOHIDDeviceClose(d->dev, kIOHIDOptionsTypeNone);
		CFRelease(d->dev);
	}
	if (d->mgr) {
		if (d->mgr_open)
			IOHIDManagerClose(d->mgr, kIOHIDOptionsTypeNone);
		CFRelease(d->mgr);
	}
	free(d);
}
