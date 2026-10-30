import assert from "node:assert/strict";
import test from "node:test";

import { parseRemoteAPI } from "./desktopServer.mjs";

test("remote API keeps HTTPS and loopback HTTP defaults", () => {
  assert.equal(parseRemoteAPI("https://api.example.test").protocol, "https:");
  assert.equal(parseRemoteAPI("http://127.0.0.1:5004").hostname, "127.0.0.1");
  assert.throws(() => parseRemoteAPI("http://10.210.10.32:5004"), /HTTPS/);
});

test("private HTTP requires an exact explicit host:port opt-in", () => {
  const policy = { insecurePrivateHost: "10.210.10.32:5004" };
  assert.equal(parseRemoteAPI("http://10.210.10.32:5004", policy).port, "5004");
  assert.throws(() => parseRemoteAPI("http://10.210.10.32:5005", policy), /HTTPS/);
  assert.throws(() => parseRemoteAPI("http://192.168.1.20:5004", policy), /HTTPS/);
  assert.throws(
    () => parseRemoteAPI("http://1.1.1.1:5004", { insecurePrivateHost: "1.1.1.1:5004" }),
    /HTTPS/,
  );
});
