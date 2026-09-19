// agentpaste.js — pegado de credenciales de antena en "+ Nuevo agente"
// (T73, ct-2026-08-27-1713, boss verbatim: "me gustaria un auto detect para
// pegar ese string completo y que detecte el nombre del agente tambien...
// que se pegue automaticamente y llene todos los campos"). Lógica PURA
// (sin document/fetch/location) separada de app.js a propósito: así se
// puede testear con Node sin stubear DOM — ver
// internal/dashboard/agentpaste_test.js (un nivel afuera de este
// directorio a propósito: //go:embed web empaqueta todo lo que hay acá
// adentro en el binario, un test no tiene nada que hacer servido por el
// dashboard), corrido con `node internal/dashboard/agentpaste_test.js`,
// sin dependencias.
// Cargado en index.html ANTES de app.js — funciones globales que app.js
// llama directo, sin namespace: un script de una sola página no necesita
// eso, y "no build step, no framework" (app.js, línea 1) sigue vigente:
// dos <script> planos, cero bundler.

// parseAgentCredentialsPaste: el string completo que capi_credentials
// produce — "<ip>:<puerto>  chat_id:<terminal_id>  pin:<pin-base64>",
// separadores de espacio variables, orden no garantizado. Devuelve null si
// el texto no tiene ninguna marca reconocible (ni "chat_id:" ni "pin:") —
// eso es lo que decide "esto es una credencial" vs. un pegado cualquiera,
// no un host:puerto suelto (demasiado ambiguo con un pegado normal en el
// campo Endpoint). Si hay marca, devuelve SOLO lo que encuentra — nunca
// inventa un campo que no está en el texto (T73: "si el string trae solo
// parte... llená lo que puedas y dejá el resto como está" es
// responsabilidad del caller, que solo pisa los campos presentes acá).
function parseAgentCredentialsPaste(text) {
  var chatIDMatch = text.match(/chat_id:(\S+)/);
  var pinMatch = text.match(/pin:(\S+)/);
  if (!chatIDMatch && !pinMatch) return null;

  var result = {};
  // El pin es base64 y puede terminar en "=" o "==" — \S+ lo toma entero,
  // "=" no es espacio, no hay split ingenuo que lo corte.
  if (pinMatch) result.pin = pinMatch[1];
  if (chatIDMatch) {
    result.terminalID = chatIDMatch[1];
    var derived = deriveAgentNameFromChatID(chatIDMatch[1]);
    if (derived) {
      result.name = derived.name;
      result.agentID = derived.agentID;
    }
  }
  // El host:puerto se busca en lo que queda después de sacar los dos
  // tokens con marca — así no hace falta asumir un orden fijo entre los
  // tres pedazos del string.
  var rest = text.replace(/chat_id:\S+/, "").replace(/pin:\S+/, "");
  var hostMatch = rest.match(/(\S+):(\d+)(?!\S)/);
  if (hostMatch) result.endpoint = "http://" + hostMatch[1] + ":" + hostMatch[2];

  return result;
}

// capitalizeFirst: la única regla de mayúscula que este archivo aplica —
// primera letra arriba, el resto tal cual viene. No intenta reproducir un
// nombre de marca con mayúsculas internas (p.ej. "CleverCoder"): eso
// necesitaría una lista de casos especiales que nadie pidió mantener —
// "el dueño edita el nombre si quiere" (más abajo) ya cubre ese caso.
function capitalizeFirst(s) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// deriveAgentNameFromChatID — capi_credentials documenta DOS formatos de
// chat_id:
//   CON identidad:  capi-<proyecto>-<agente>-<verificador>
//   SIN identidad:  capi-<proyecto>-<verificador>-<N>   (N: número corto)
//
// TRAMPA: <proyecto> puede llevar guiones (piumy-gateway tiene dos
// segmentos) — parsear contando segmentos DESDE EL PRINCIPIO rompe. Se
// parsea desde el FINAL: si el último segmento es un número, es el
// formato SIN identidad (no hay nombre que sacar — nunca se inventa uno a
// partir del verificador, T140: un nombre inventado es peor que un campo
// vacío porque parece correcto); si no, el agente es el PENÚLTIMO
// segmento y el proyecto es todo lo que queda entre "capi" y el agente
// (uno o más segmentos, unidos con "-" tal cual estaban).
//
// name (T140, ct-2026-09-05-1607 — el dueño: "que la antena le ponga el
// nombre + proyecto y agrege mayuscula inial") combina agente + proyecto,
// "Agente Proyecto" — agentID SIGUE siendo solo el agente (nunca cambió,
// es la clave que agent-create usa; el contrato pide completar lo que se
// VE, no ensanchar el identificador).
//
// ponytail: un nombre de agente o de proyecto que él mismo lleve guiones
// internos se detecta con esos guiones tal cual (nunca invento un
// título-por-palabra). No se resuelve más allá de eso — el dueño ya dijo
// que edita el nombre si hace falta; adivinar de más acá cuesta más de lo
// que ahorra.
function deriveAgentNameFromChatID(chatID) {
  var segments = chatID.split("-");
  if (segments.length < 2) return null;
  var last = segments[segments.length - 1];
  if (/^\d+$/.test(last)) return null; // formato sin identidad
  var agentIndex = segments.length - 2;
  var raw = segments[agentIndex];
  if (!raw) return null;
  var name = capitalizeFirst(raw);
  var projectSegments = segments.slice(1, agentIndex);
  if (projectSegments.length) {
    name += " " + capitalizeFirst(projectSegments.join("-"));
  }
  return { name: name, agentID: raw.toLowerCase() };
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { parseAgentCredentialsPaste: parseAgentCredentialsPaste, deriveAgentNameFromChatID: deriveAgentNameFromChatID };
}
