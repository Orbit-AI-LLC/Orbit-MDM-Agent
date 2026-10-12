// Bridges the system zlib into Swift as the CZlib module. The RFB/VNC server
// needs a persistent per-session zlib stream flushed with Z_SYNC_FLUSH per frame
// (level 6), which Compression.framework cannot reproduce; this exposes the real
// libz deflate API instead.
#include <zlib.h>
