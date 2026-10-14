#ifndef UPSHID_DARWIN_H
#define UPSHID_DARWIN_H

// Status codes returned alongside raw IOReturn values (which are all 0xe00002xx).
enum {
	UPSHID_OK = 0,
	UPSHID_ERR_NOT_FOUND = 1,
	UPSHID_ERR_TIMEOUT = 2,
	UPSHID_ERR_REMOVED = 3,
};

typedef struct upshid_dev upshid_dev;

upshid_dev *upshid_open(int vid, int pid, int *status);
int upshid_exchange(upshid_dev *d, const char *cmd, int cmdlen, char *out, int outcap, int *outlen, double timeout);
void upshid_close(upshid_dev *d);

#endif
