# Minimal simple-query wire client: prints every backend message type of interest.
import socket, struct, sys
def run(sql, user="postgres", port=55432):
    s = socket.create_connection(("127.0.0.1", port))
    params = b"user\0" + user.encode() + b"\0database\0postgres\0\0"
    s.sendall(struct.pack("!ii", 8 + len(params), 196608) + params)
    buf = b""
    def msgs():
        nonlocal buf
        while True:
            while len(buf) >= 5:
                t = buf[0:1]; n = struct.unpack("!i", buf[1:5])[0]
                if len(buf) < 1 + n: break
                body = buf[5:1+n]; buf = buf[1+n:]; yield t, body
            buf += s.recv(65536)
    it = msgs()
    for t, b in it:
        if t == b"Z": break
    s.sendall(b"Q" + struct.pack("!i", 4 + len(sql.encode()) + 1) + sql.encode() + b"\0")
    print(">>>", sql.replace("\n", "\\n"))
    for t, b in it:
        if t == b"C": print("   CommandComplete", b.rstrip(b"\0").decode())
        elif t == b"S": k, v = b.split(b"\0")[:2]; print("   ParameterStatus", k.decode(), "=", v.decode())
        elif t == b"E" or t == b"N":
            f = dict((x[:1], x[1:]) for x in b.split(b"\0") if x)
            print("   " + ("Error" if t == b"E" else "Notice"), f.get(b"C", b"").decode(), f.get(b"M", b"").decode())
        elif t == b"A": print("   NotificationResponse")
        elif t == b"Z": print("   ReadyForQuery", b.decode()); break
    s.close()
for q in sys.argv[1:]: run(q)
