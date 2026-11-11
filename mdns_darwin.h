#ifndef AXIS_IP_MDNS_DARWIN_H
#define AXIS_IP_MDNS_DARWIN_H

#include <stdint.h>

// axBrowse browses regtype for timeout_ms and reports results to the Go callbacks.
// Returns a DNSServiceErrorType (0 on success).
int axBrowse(const char *regtype, int timeout_ms, uintptr_t handle);

#endif
