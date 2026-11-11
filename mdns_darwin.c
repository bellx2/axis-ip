// Browse -> Resolve -> GetAddrInfo through the mDNSResponder DNS-SD API.
// All operations share one connection, driven by select until the timeout.

#include <dns_sd.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/select.h>
#include <sys/time.h>
#include <string.h>
#include "mdns_darwin.h"
#include "_cgo_export.h"

typedef struct {
    DNSServiceRef conn;
    uintptr_t handle;
} axCtx;

static void DNSSD_API addrCB(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
                             DNSServiceErrorType err, const char *hostname,
                             const struct sockaddr *addr, uint32_t ttl, void *context) {
    axCtx *ctx = (axCtx *)context;
    if (err != kDNSServiceErr_NoError || !(flags & kDNSServiceFlagsAdd) || addr == NULL) {
        return;
    }
    if (addr->sa_family != AF_INET) {
        return;
    }
    char buf[INET_ADDRSTRLEN];
    const struct sockaddr_in *sin = (const struct sockaddr_in *)addr;
    if (inet_ntop(AF_INET, &sin->sin_addr, buf, sizeof(buf)) == NULL) {
        return;
    }
    goAddr(ctx->handle, (char *)hostname, buf);
}

static void DNSSD_API resolveCB(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
                                DNSServiceErrorType err, const char *fullname,
                                const char *hosttarget, uint16_t port, uint16_t txtLen,
                                const unsigned char *txt, void *context) {
    axCtx *ctx = (axCtx *)context;
    if (err != kDNSServiceErr_NoError) {
        return;
    }
    // Read macaddress from the TXT record.
    char mac[64] = {0};
    uint8_t vlen = 0;
    const void *v = TXTRecordGetValuePtr(txtLen, txt, "macaddress", &vlen);
    if (v != NULL && vlen < sizeof(mac)) {
        memcpy(mac, v, vlen);
    }
    goResolved(ctx->handle, (char *)fullname, (char *)hosttarget, ntohs(port), mac);

    DNSServiceRef child = ctx->conn;
    DNSServiceGetAddrInfo(&child, kDNSServiceFlagsShareConnection, ifIndex,
                          kDNSServiceProtocol_IPv4, hosttarget, addrCB, ctx);
}

static void DNSSD_API browseCB(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifIndex,
                               DNSServiceErrorType err, const char *name, const char *regtype,
                               const char *domain, void *context) {
    axCtx *ctx = (axCtx *)context;
    if (err != kDNSServiceErr_NoError || !(flags & kDNSServiceFlagsAdd)) {
        return;
    }
    char full[kDNSServiceMaxDomainName];
    if (DNSServiceConstructFullName(full, name, regtype, domain) != kDNSServiceErr_NoError) {
        return;
    }
    goBrowsed(ctx->handle, (char *)name, full, ifIndex);

    DNSServiceRef child = ctx->conn;
    DNSServiceResolve(&child, kDNSServiceFlagsShareConnection, ifIndex, name, regtype, domain,
                      resolveCB, ctx);
}

static long long nowMs(void) {
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return (long long)tv.tv_sec * 1000 + tv.tv_usec / 1000;
}

int axBrowse(const char *regtype, int timeout_ms, uintptr_t handle) {
    axCtx ctx = {0};
    ctx.handle = handle;

    DNSServiceErrorType err = DNSServiceCreateConnection(&ctx.conn);
    if (err != kDNSServiceErr_NoError) {
        return err;
    }
    DNSServiceRef browse = ctx.conn;
    err = DNSServiceBrowse(&browse, kDNSServiceFlagsShareConnection, 0, regtype, "local.",
                           browseCB, &ctx);
    if (err != kDNSServiceErr_NoError) {
        DNSServiceRefDeallocate(ctx.conn);
        return err;
    }

    int fd = DNSServiceRefSockFD(ctx.conn);
    long long deadline = nowMs() + timeout_ms;
    for (;;) {
        long long left = deadline - nowMs();
        if (left <= 0) {
            break;
        }
        fd_set rfds;
        FD_ZERO(&rfds);
        FD_SET(fd, &rfds);
        struct timeval tv = {(time_t)(left / 1000), (suseconds_t)((left % 1000) * 1000)};
        int n = select(fd + 1, &rfds, NULL, NULL, &tv);
        if (n < 0) {
            continue; // EINTR and similar
        }
        if (n == 0) {
            break;
        }
        err = DNSServiceProcessResult(ctx.conn);
        if (err != kDNSServiceErr_NoError) {
            break;
        }
    }
    // Deallocating the shared connection also frees every child operation.
    DNSServiceRefDeallocate(ctx.conn);
    return err;
}
