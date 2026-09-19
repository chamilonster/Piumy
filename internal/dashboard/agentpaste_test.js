// agentpaste_test.js (T73, ct-2026-08-27-1713) — chequeo del parseo, sin
// framework: `node internal/dashboard/agentpaste_test.js`. Vive FUERA de
// web/ a propósito: //go:embed web (embed.go) empaqueta todo lo que hay
// ahí adentro en el binario, y un script de test no tiene nada que hacer
// servido por el dashboard. Los valores de host/chat_id/pin acá son
// SINTÉTICOS a propósito (constitución §2b) — nunca la credencial real
// del dueño, que va redactada del contrato.
"use strict";
var assert = require("assert");
var lib = require("./web/agentpaste.js");
var parseAgentCredentialsPaste = lib.parseAgentCredentialsPaste;
var deriveAgentNameFromChatID = lib.deriveAgentNameFromChatID;

function test(name, fn) {
  fn();
  console.log("ok - " + name);
}

// T140 (ct-2026-09-05-1607): name pasa a ser "Agente Proyecto", no solo
// el agente — el dueño: "que la antena le ponga el nombre + proyecto".
// agentID SIGUE siendo solo el agente (la clave de agent-create, sin
// cambios).
test("proyecto con guiones (piumy-gateway) — nombre combina agente + proyecto", function () {
  var got = deriveAgentNameFromChatID("capi-piumy-gateway-citrino-a1b2c3d4");
  assert.deepStrictEqual(got, { name: "Citrino Piumy-gateway", agentID: "citrino" });
});

test("proyecto de un solo segmento (T140, ct-2026-09-05-1607 — el ejemplo del contrato)", function () {
  var got = deriveAgentNameFromChatID("capi-clevercoder-citrino-caaed305");
  assert.deepStrictEqual(got, { name: "Citrino Clevercoder", agentID: "citrino" });
});

test("formato sin identidad (último segmento numérico) — sin nombre", function () {
  var got = deriveAgentNameFromChatID("capi-piumy-gateway-a1b2c3d4-7");
  assert.strictEqual(got, null);
});

test("string completo, pin terminado en '=' — los 5 campos", function () {
  var text = "192.168.1.77:8788  chat_id:capi-piumy-gateway-citrino-a1b2c3d4  pin:c3VwZXJzZWNyZXQ=";
  var got = parseAgentCredentialsPaste(text);
  assert.deepStrictEqual(got, {
    pin: "c3VwZXJzZWNyZXQ=",
    terminalID: "capi-piumy-gateway-citrino-a1b2c3d4",
    name: "Citrino Piumy-gateway",
    agentID: "citrino",
    endpoint: "http://192.168.1.77:8788",
  });
});

test("pin terminado en '==' — no se corta con split por '='", function () {
  var got = parseAgentCredentialsPaste("10.0.0.5:9000 chat_id:capi-piumy-gateway-vendedor1-x9y8z7 pin:YWJjZA==");
  assert.strictEqual(got.pin, "YWJjZA==");
});

test("espacios de más entre los tres pedazos", function () {
  var text = "  192.168.1.77:8788     chat_id:capi-piumy-gateway-citrino-a1b2c3d4      pin:c3VwZXI=  ";
  var got = parseAgentCredentialsPaste(text);
  assert.strictEqual(got.endpoint, "http://192.168.1.77:8788");
  assert.strictEqual(got.terminalID, "capi-piumy-gateway-citrino-a1b2c3d4");
  assert.strictEqual(got.pin, "c3VwZXI=");
});

test("formato sin identidad dentro del string completo — sin nombre/id, resto sí", function () {
  var got = parseAgentCredentialsPaste("192.168.1.77:8788 chat_id:capi-piumy-gateway-a1b2c3d4-7 pin:c3VwZXI=");
  assert.strictEqual(got.name, undefined);
  assert.strictEqual(got.agentID, undefined);
  assert.strictEqual(got.terminalID, "capi-piumy-gateway-a1b2c3d4-7");
  assert.strictEqual(got.endpoint, "http://192.168.1.77:8788");
});

test("pegado parcial (sin pin) — llena lo que hay, pin queda ausente", function () {
  var got = parseAgentCredentialsPaste("192.168.1.77:8788 chat_id:capi-piumy-gateway-citrino-a1b2c3d4");
  assert.strictEqual(got.pin, undefined);
  assert.strictEqual(got.terminalID, "capi-piumy-gateway-citrino-a1b2c3d4");
  assert.strictEqual(got.endpoint, "http://192.168.1.77:8788");
});

test("pegado que NO es una credencial — null, pasa de largo", function () {
  assert.strictEqual(parseAgentCredentialsPaste("Juan Pérez"), null);
  assert.strictEqual(parseAgentCredentialsPaste("192.168.1.77:8788"), null); // host:puerto solo, sin marca — ambiguo, no se toca
});

console.log("agentpaste_test.js: todo verde");
