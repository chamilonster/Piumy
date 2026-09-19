; Piumy — instalador de Windows (ct-2026-07-31-1643, boss verbatim: "quiero
; que subas el primer instalador para windows a github, como piumy
; windows"). Inno Setup, autorizado ("1 si 2 publico 3 solo el instalador"
; — repo público, se sube SOLO este instalador compilado, nunca el código
; fuente ni esta fuente misma sin querer).
;
; La vara: alguien que nunca vio esto hace doble clic y termina con Piumy
; andando y el QR en pantalla, sin escribir una sola variable de entorno
; ni inventar una sola clave. Hoy eso pide un .bat a mano con nueve
; variables y tres claves inventadas por una persona — este script es lo
; que saca ese muro.
;
; Compilar: ISCC.exe installer\windows\piumy.iss (desde coderoot/<worktree>,
; con dist\piumy-gateway-windows-amd64.exe ya construido — ver build-all.sh
; o el comando equivalente para windows/amd64).
;
; version.iss (junto a este archivo) NO se versiona — build-all.sh lo genera
; en cada corrida a partir de VERSION (raíz del repo), la única fuente del
; número. Inno Setup no tiene una forma cómoda de leer un archivo de texto
; plano como valor, así que el intermediario es este #include con un
; #define adentro. Compilar este .iss sin haber corrido build-all.sh antes
; falla — mismo requisito que ya tenía dist\piumy-gateway-windows-amd64.exe.
#include "version.iss"

#define MyAppName "Piumy"
#define MyAppPublisher "Piumy"
#define MyAppExeName "Piumy.exe"
#define MyBuiltExe "..\..\dist\piumy-gateway-windows-amd64.exe"
; T54 (ct-2026-08-10-1934): SHA-256 del binario que este instalador
; empaqueta, calculado en TIEMPO DE COMPILACIÓN (ISPP's GetSHA256OfFile —
; distinto de Pascal Scripting's propia GetSHA256OfFile, misma firma, dos
; motores). CurStepChanged compara esto contra el hash del archivo que
; QUEDÓ en disco tras instalar — no la versión (el binario no trae
; VersionInfo embebido, medido: 0.0.0.0) ni la fecha (reinstalar la misma
; build no la cambia, lo medido antes de escribir esto). Responde la
; única pregunta que importa: "¿el que quedó es el que traía este
; instalador?" — y de paso detecta un archivo corrupto o a medio copiar,
; no solo uno viejo.
#define MyBuiltExeHash GetSHA256OfFile(MyBuiltExe)

[Setup]
; GUID fijo generado una sola vez (2026-07-31) — Inno lo usa para
; identificar ESTA aplicación entre versiones/reinstalaciones. No
; regenerar en compilaciones futuras del mismo producto.
AppId={{D3381EC7-083F-4E31-8C6A-537633049949}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
; Programa y datos juntos (decisión de Citrino): un solo lugar, sin
; Program Files, sin elevación — el programa escribe su propia base de
; datos al lado del ejecutable.
DefaultDirName={localappdata}\Piumy
DisableProgramGroupPage=yes
; PrivilegesRequired=lowest: NUNCA pide administrador — parte del
; criterio de listo ("instala sin pedir administrador").
PrivilegesRequired=lowest
; T21 (ct-2026-08-05-1308, secundario): sin esto, reinstalar/desinstalar
; con Piumy corriendo da el diálogo "archivo en uso" de Windows para
; Piumy.exe — "Ignorar" ahí deja el ejecutable VIEJO corriendo emparejado
; con el launcher/claves NUEVOS. El nombre tiene que matchear EXACTO al
; mutex que crea el propio binario (appMutexName, appmutex_windows.go) —
; Setup y Uninstall lo chequean solos, nada más en este script lo toca.
AppMutex=PiumyGatewaySingleInstanceMutex
OutputDir=..\..\dist
OutputBaseFilename=Piumy-Setup-{#MyAppVersion}
SetupIconFile=..\..\assets\app.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
; Bienvenida (boss verbatim: "falta una pantallita de bienvenida al
; installer, carita bonita, que es piumi") — la carita del dashboard
; (IDLE_FACE_HTML, app.js) rasterizada a .bmp: mismos colores exactos
; (--bg2/--panel de fondo, --phos para la carita, --green para "piumy"),
; ver assets/wizard-welcome.bmp. DisableWelcomePage explícito en no: el
; texto se pone en InitializeWizard, WelcomeLabel1/2.
DisableWelcomePage=no
WizardImageFile=..\..\assets\wizard-welcome.bmp
; Descargo de responsabilidad (boss verbatim: "un eula de que no nos
; responsabilizamos de nada") — página estándar de Inno con aceptar/rechazar,
; texto de Citrino en eula.txt, sin tocar.
LicenseFile=eula.txt
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
; 0.1.0 fue la prueba interna (instalada y probada en la máquina del boss,
; sin las reglas visibles/aprobador/contactos por evento). 0.1.1 es el
; primero público, desde master a93e29b — Citrino: no reusar un número ya
; probado con contenido distinto, se pierde la trazabilidad de qué tiene
; quién. 0.1.2 (T4, ct-2026-08-05-0256) cambia el CONTENIDO del paquete —
; suma la 4ta clave (PIUMY_CAPI_KEY) y agentclient.exe — así que la
; versión se mueve, mismo criterio de arriba. 0.1.3 (T11,
; ct-2026-08-05-1214) saca el .bat/.vbs del camino de arranque — los
; accesos directos lanzan Piumy.exe directo, la config va a
; piumy-config.json — mismo criterio, cambia lo que el paquete instala.
; 0.1.4 (T28, ct-2026-08-05-2242, decisión del boss) saca la 4ta clave y
; agentclient.exe — el despacho ya no viaja con una segunda capa de
; cifrado propia, el túnel de cAPI (CleverCoder) alcanza. Vuelve a ser un
; paquete de 3 claves — mismo criterio de siempre, cambia lo que instala.
; 0.1.5 (ct-2026-08-06, pedido del boss) recompila por dos manuales
; go:embed actualizados (connect/SKILL.md, orchestrator/operacion.md) —
; no cambia qué instala el paquete, cambia el binario embebido. Publicada
; ya con esa versión; este commit pone el número en `VERSION` (raíz del
; repo) como fuente única — antes solo vivía acá y en el literal de
; server.go, y las dos copias se habían desincronizado. 0.1.6 recompila
; con la versión ya unificada. 0.1.7 (T34, ct-2026-08-07, bug reportado
; por el boss con captura) arregla el instalador: no cerraba solo el
; Piumy corriendo (pedía al usuario que lo hiciera a mano) y en modo
; desatendido devolvía éxito sin instalar nada — ver CloseRunningInstance
; más abajo. No cambia qué instala el paquete, cambia CÓMO se instala.
VersionInfoVersion={#MyAppVersion}

[Languages]
; Todo el producto (dashboard, manuales) está en español — el instalador
; sigue el mismo idioma, sin agregar inglés que nadie pidió.
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Files]
Source: "{#MyBuiltExe}"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion

[Icons]
; T11 (ct-2026-08-05-1214, boss verbatim: "se nota que se ejecuta un bat o
; algo, se puede hacer eso silencioso?"): los dos accesos directos apuntan
; a Piumy.exe DIRECTO — ya no hay .bat ni .vbs en el camino de arranque.
; Antes: .vbs -> cmd.exe corriendo run-piumy.bat (solo para poner
; variables de entorno) -> Piumy.exe. Diagnóstico verificado por
; eliminación: Piumy.exe es subsystem GUI (no abre consola sola), los dos
; .lnk reales del boss apuntaban al .vbs — quedaba un solo candidato, el
; cmd.exe que corría el .bat. En Windows 11 el host de consola por
; defecto (Windows Terminal) no siempre respeta el pedido de ventana
; oculta — el parpadeo negro que vio el boss. Piumy.exe ahora lee su
; configuración de piumy-config.json (config.ApplyFileDefaults, Go) —
; sin script de shell en el medio, sin parpadeo, y viaja a Linux/Mac/
; Raspberry (un .bat de Windows no). Sin IconFilename: el ícono de
; Piumy.exe ya es el correcto, no hace falta pedirlo aparte.
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"
Name: "{userstartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; Tasks: startupicon

[Tasks]
Name: "startupicon"; Description: "Arrancar Piumy automáticamente al iniciar Windows"; Flags: unchecked

[Code]
var
  PasswordPage: TInputQueryWizardPage;
  { T21 (ct-2026-08-05-1308): resueltas en PrepareToInstall (ANTES de
    copiar un solo archivo) y reusadas tal cual en CurStepChanged — ver
    el porqué en el doc de PrepareToInstall. }
  ResolvedMcpKey, ResolvedRestKey, ResolvedBackupKey, ResolvedRestAddr: String;

{ ── Aleatoriedad real: CoCreateGuid (ole32.dll), no el generador
    pseudo-random de Pascal Script. Las claves protegen el canal del agente,
    la administración por red (PIUMY_REST_KEY, la más importante: sin ella
    la administración queda abierta a toda la red) y el respaldo cifrado —
    necesitan aleatoriedad de verdad, no una semilla predecible.

    Antes probamos CryptAcquireContextW+CryptGenRandom (advapi32.dll) — dos
    formas distintas de pasarle el buffer (array of Byte tipado, y después
    AnsiString), y las dos revientan con Access Violation en el MISMO offset
    del binario entre builds con código distinto adentro de la función. Eso
    dice que el problema no es el tipo del buffer del lado de este script:
    es la propia marshaling nativa de Pascal Script para esa forma de
    llamada externa (var LongWord de salida + varios LongWord más). Reproducido
    en vivo (ct-2026-07-31-1643): con esas llamadas, revienta; con ellas
    sacadas, instala de punta a punta sin error. CoCreateGuid tiene una
    firma mucho más simple (un solo parámetro, TGUID) y TGUID es un tipo
    explícitamente soportado por Pascal Script — evita el camino que
    crashea. La aleatoriedad de CoCreateGuid sigue siendo real: Windows la
    genera con su generador criptográfico, no con un timestamp. }
function CoCreateGuid(var guid: TGUID): HResult;
  external 'CoCreateGuid@ole32.dll stdcall';
function SetEnvironmentVariableW(lpName: String; lpValue: String): BOOL;
  external 'SetEnvironmentVariableW@kernel32.dll stdcall';

function GuidHex(g: TGUID): String;
var
  HexChars: String;
  i: Integer;
  b: Byte;
begin
  HexChars := '0123456789abcdef';
  Result := '';
  for i := 0 to 3 do
  begin
    b := (g.D1 shr (i * 8)) and $FF;
    Result := Result + HexChars[(b shr 4) + 1] + HexChars[(b and $0F) + 1];
  end;
  for i := 0 to 1 do
  begin
    b := (g.D2 shr (i * 8)) and $FF;
    Result := Result + HexChars[(b shr 4) + 1] + HexChars[(b and $0F) + 1];
  end;
  for i := 0 to 1 do
  begin
    b := (g.D3 shr (i * 8)) and $FF;
    Result := Result + HexChars[(b shr 4) + 1] + HexChars[(b and $0F) + 1];
  end;
  for i := 0 to 7 do
  begin
    b := g.D4[i];
    Result := Result + HexChars[(b shr 4) + 1] + HexChars[(b and $0F) + 1];
  end;
end;

{ ErrMsg sale '' en éxito — T22 (ct-2026-08-05-1455, menor 1): antes usaba
  RaiseException directo, y esta función corre DENTRO de PrepareToInstall
  desde T21 (ResolveLauncherKeys la llama para cada clave a generar) —
  exactamente el hook que el propio T21 documentó que RaiseException NO
  aborta limpio desde ciertos contextos (ver el doc de PrepareToInstall),
  y este call site nunca se probó desde ahí. Probabilidad de que
  CoCreateGuid falle: ínfima — pero el arreglo es barato, y es el mismo
  mecanismo (string de error, no excepción) que ya usa el resto de
  ResolveLauncherKeys. }
function GenerateRandomHex(NumBytes: Integer; var ErrMsg: String): String;
var
  g1, g2: TGUID;
  Full: String;
begin
  ErrMsg := '';
  Result := '';
  if CoCreateGuid(g1) <> 0 then
  begin
    ErrMsg := 'Piumy: no se pudo generar una clave aleatoria (CoCreateGuid).';
    Exit;
  end;
  if CoCreateGuid(g2) <> 0 then
  begin
    ErrMsg := 'Piumy: no se pudo generar una clave aleatoria (CoCreateGuid).';
    Exit;
  end;
  { 2 GUIDs = 32 bytes = 64 hex chars, alcanza para la clave más larga
    (BackupKey, 32 bytes); las más cortas truncan el mismo material. }
  Full := GuidHex(g1) + GuidHex(g2);
  Result := Copy(Full, 1, NumBytes * 2);
end;

{ TryGenerateRandomHex: wrapper de conveniencia para ResolveLauncherKeys —
  devuelve el mensaje de error ('' en éxito) y deja el valor en Key, mismo
  shape que ResolveLauncherKeys para poder propagar con `if Result <> ''
  then Exit;` sin repetir el manejo de ErrMsg en cada uno de los 5 sitios
  que generan una clave. }
function TryGenerateRandomHex(NumBytes: Integer; var Key: String): String;
begin
  Key := GenerateRandomHex(NumBytes, Result);
end;

{ ── T6 (ct-2026-08-05-0315): reinstalar NUNCA puede generar una clave
    nueva si ya hay una — BackupKey nueva vuelve ilegibles para siempre
    los backups cifrados con la vieja; McpKey/RestKey nuevas descablean
    todo lo que ya apuntaba a las viejas. La única fuente que tenía una
    instalación existente en ese momento era el propio run-piumy.bat —
    agent-connect.json todavía no existía en 0.1.1, y ni ahí ni acá se
    guarda la BackupKey aparte. T6 dejó anotado un ponytail: "parsear el
    .bat a mano es feo... el camino de upgrade es mover las 4 claves a un
    archivo de config propio" — DEUDA PAGADA por T11 (ct-2026-08-05-1214):
    piumy-config.json (más abajo, ResolveKeys) es ese archivo; el .bat
    sigue siendo una fuente de MIGRACIÓN válida (una instalación vieja que
    todavía no actualizó a T11), pero ya no es donde viven las claves de
    una instalación nueva.

    T21 (ct-2026-08-05-1308, R2 de Amatista, CRITICAL): el parser de abajo
    reemplaza por completo al de T6 — ese exigía un match exacto de
    "set VAR=" y, si fallaba (Notepad guardando el .bat como "Unicode" es
    el caso real que Amatista describió), se quedaba en silencio sin
    reusar nada: KeyOrGenerate generaba una clave nueva y el launcher
    nuevo pisaba al viejo, sin log, sin aviso — los backups cifrados con
    la clave vieja quedaban ilegibles para siempre. La regla que fija
    Citrino: "ante la duda, no se pisa nada" — si hay un launcher previo
    y no se puede leer sus claves con certeza, la instalación se detiene
    con RaiseException (mismo mecanismo que NextButtonClick ya usa más
    abajo para /DASHBOARDPASSWORD inválido — verificado ahí que da un
    código de salida distinto de cero y dispara el mensaje al log incluso
    en modo silencioso, así que abortar duele igual con o sin UI). }

{ HasBOM: detecta una marca de orden de bytes (UTF-16 LE/BE, o UTF-8 con
  marca) al principio del archivo — LEÍDO COMO BYTES CRUDOS (AnsiString,
  no String: en Inno Setup 6 String es Unicode, y una conversión
  implícita ANTES de este chequeo podría transformar o perder justo los
  bytes que hacen falta para reconocer la marca). Cualquiera de las tres
  aborta: nuestro propio SaveStringToFile (más abajo) nunca escribe una
  marca — su presencia significa que algo más tocó este archivo. }
function HasBOM(const Raw: AnsiString): Boolean;
begin
  Result := False;
  if Length(Raw) >= 2 then
  begin
    if (Ord(Raw[1]) = $FF) and (Ord(Raw[2]) = $FE) then
      Result := True; { UTF-16 LE — "Unicode" en el selector de Notepad }
    if (Ord(Raw[1]) = $FE) and (Ord(Raw[2]) = $FF) then
      Result := True; { UTF-16 BE }
  end;
  if Length(Raw) >= 3 then
    if (Ord(Raw[1]) = $EF) and (Ord(Raw[2]) = $BB) and (Ord(Raw[3]) = $BF) then
      Result := True; { UTF-8 con BOM }
end;

{ TrimStr: espacios y control chars (incluido \r colgado) al principio y
  al final — propio en vez de confiar en el Trim del motor, para no
  depender de qué caracteres considera "espacio". }
function TrimStr(const S: String): String;
var
  StartI, EndI: Integer;
begin
  StartI := 1;
  EndI := Length(S);
  while (StartI <= EndI) and (S[StartI] <= ' ') do
    StartI := StartI + 1;
  while (EndI >= StartI) and (S[EndI] <= ' ') do
    EndI := EndI - 1;
  if EndI >= StartI then
    Result := Copy(S, StartI, EndI - StartI + 1)
  else
    Result := '';
end;

{ SplitLines: separa por \n (tolerando un \r colgado de CRLF), a mano —
  reemplaza LoadStringsFromFile, que Amatista encontró que no distingue
  UTF-16 de basura (acá ya no importa: HasBOM aborta antes de llegar a
  parsear una sola línea). }
function SplitLines(const Content: String): TArrayOfString;
var
  S, Line: String;
  P, N: Integer;
begin
  SetArrayLength(Result, 0);
  S := Content;
  N := 0;
  while Length(S) > 0 do
  begin
    P := Pos(#10, S);
    if P = 0 then
    begin
      Line := S;
      S := '';
    end
    else
    begin
      Line := Copy(S, 1, P - 1);
      S := Copy(S, P + 1, MaxInt);
    end;
    if (Length(Line) > 0) and (Line[Length(Line)] = #13) then
      Line := Copy(Line, 1, Length(Line) - 1);
    SetArrayLength(Result, N + 1);
    Result[N] := Line;
    N := N + 1;
  end;
end;

{ ParseSetLine: parser TOLERANTE de una línea "set VAR=valor" — mayúscula
  o minúscula en "set", espacios alrededor del "=", comillas alrededor de
  todo "VAR=valor" (la forma que recomienda cmd para evitar espacios
  colgando) o solo alrededor del valor. True + Value si la línea define
  VarName. }
function ParseSetLine(const Line, VarName: String; var Value: String): Boolean;
var
  T, Rest, Key, V: String;
  EqPos: Integer;
begin
  Result := False;
  T := TrimStr(Line);
  if Lowercase(Copy(T, 1, 4)) <> 'set ' then
    Exit;
  Rest := TrimStr(Copy(T, 5, MaxInt));
  if Rest = '' then
    Exit;

  if (Length(Rest) >= 2) and (Rest[1] = '"') and (Rest[Length(Rest)] = '"') then
    Rest := Copy(Rest, 2, Length(Rest) - 2);

  EqPos := Pos('=', Rest);
  if EqPos = 0 then
    Exit;
  Key := TrimStr(Copy(Rest, 1, EqPos - 1));
  { T22 (ct-2026-08-05-1455, R3 de Amatista): el valor NUNCA se despoja de
    comillas — cmd no lo hace. Verificado ejecutando cmd de verdad:
    `set VAR="x"` deja la variable CON comillas (`["x"]`); solo
    `set "VAR=x"` (arriba, comillas envolviendo TODO name=value) las saca,
    porque ahí las comillas nunca fueron parte del valor. Sacarlas acá
    reusaba una clave DISTINTA a la que la app realmente tenía en memoria
    — sin abortar, porque la clave "se encontraba". Replicar cmd exacto:
    si el valor real tiene comillas, ese es el valor. }
  V := TrimStr(Copy(Rest, EqPos + 1, MaxInt));

  if Lowercase(Key) <> Lowercase(VarName) then
    Exit;

  Value := V;
  Result := True;
end;

function BoolStr(B: Boolean): String;
begin
  if B then
    Result := 'sí'
  else
    Result := 'no';
end;

{ ── T11 (ct-2026-08-05-1214): piumy-config.json es el archivo de ENTRADA
    nuevo — Piumy lo lee al arrancar (config.ApplyFileDefaults, Go). NO es
    agent-connect.json: ese lo escribe el gateway para que lo lea un
    agente (dirección opuesta) — archivos distintos, a propósito, nunca se
    mezclan (advertencia explícita del contrato). Forma fija, un par
    "CLAVE": "valor" por línea (así lo escribe CurStepChanged más abajo) —
    por eso JSONEscape/ReadConfigJSONValue no son un parser JSON general,
    alcanza con controlar las dos puntas (quién lo escribe y quién lo
    relee acá, en la reinstalación). }
function JSONEscape(const S: String): String;
var
  i: Integer;
  c: Char;
begin
  Result := '';
  for i := 1 to Length(S) do
  begin
    c := S[i];
    if (c = '\') or (c = '"') then
      Result := Result + '\' + c
    else
      Result := Result + c;
  end;
end;

function JSONUnescape(const S: String): String;
var
  i: Integer;
begin
  Result := '';
  i := 1;
  while i <= Length(S) do
  begin
    if (S[i] = '\') and (i < Length(S)) then
    begin
      Result := Result + S[i + 1];
      i := i + 2;
    end
    else
    begin
      Result := Result + S[i];
      i := i + 1;
    end;
  end;
end;

function ReadConfigJSONValue(const Lines: TArrayOfString; const KeyName: String): String;
var
  i, ColonPos, FirstQuote, LastQuote, j: Integer;
  Line, Rest: String;
begin
  Result := '';
  for i := 0 to GetArrayLength(Lines) - 1 do
  begin
    Line := TrimStr(Lines[i]);
    if Copy(Line, 1, Length(KeyName) + 2) <> '"' + KeyName + '"' then
      Continue;
    ColonPos := Pos(':', Line);
    if ColonPos = 0 then
      Continue;
    Rest := TrimStr(Copy(Line, ColonPos + 1, MaxInt));
    FirstQuote := Pos('"', Rest);
    if FirstQuote = 0 then
      Continue;
    LastQuote := 0;
    for j := Length(Rest) downto FirstQuote + 1 do
    begin
      if Rest[j] = '"' then
      begin
        LastQuote := j;
        Break;
      end;
    end;
    if LastQuote = 0 then
      Continue;
    Result := JSONUnescape(Copy(Rest, FirstQuote + 1, LastQuote - FirstQuote - 1));
    Exit;
  end;
end;

{ ConfigHasKey: mismo chequeo que ReadConfigJSONValue usa para reconocer
  una línea de KeyName, sin extraer su valor — lo único que
  CompleteConfigJSON necesita para decidir "¿esta clave ya está?". }
function ConfigHasKey(const Lines: TArrayOfString; const KeyName: String): Boolean;
var
  i: Integer;
  Line: String;
begin
  Result := False;
  for i := 0 to GetArrayLength(Lines) - 1 do
  begin
    Line := TrimStr(Lines[i]);
    if Copy(Line, 1, Length(KeyName) + 2) = '"' + KeyName + '"' then
    begin
      Result := True;
      Exit;
    end;
  end;
end;

{ CompleteConfigJSON: T51 (ct-2026-08-10-1826) — actualizar reescribía
  piumy-config.json entero desde la plantilla fija de este instalador, así
  que cualquier clave que el usuario hubiera agregado a mano
  (PIUMY_DEFAULT_TERMINAL_ID es el caso real que lo hizo visible)
  desaparecía sin aviso en la próxima instalación. ExistingRaw es el
  archivo que YA estaba en disco; NewJSON es la plantilla completa que
  este instalador arma más abajo (mismo texto de siempre). Devuelve
  ExistingRaw con las claves de NewJSON que le falten agregadas ANTES del
  cierre — nunca toca una línea que el archivo ya tenía, sea una clave
  conocida por este instalador o no. Si el archivo ya tiene las 9/10
  claves, es un no-op textual (ni siquiera reordena).

  No es un parser JSON — mismo espíritu que ReadConfigJSONValue (el propio
  .iss ya advierte no inventar uno en Pascal, T11): trabaja línea por
  línea, sobre el formato fijo (un par por línea) que este mismo bloque
  escribe siempre. }
function CompleteConfigJSON(const ExistingRaw, NewJSON: String): String;
var
  ExistingLines, NewLines, ToAdd: TArrayOfString;
  i, closeIdx, addCount, quotePos: Integer;
  Trimmed, KeyName, LastContentLine, Output: String;
begin
  ExistingLines := SplitLines(ExistingRaw);
  NewLines := SplitLines(NewJSON);

  { Recolectar, de la plantilla, las líneas "CLAVE": "valor" cuya CLAVE
    todavía no esté en el archivo existente. }
  SetArrayLength(ToAdd, 0);
  addCount := 0;
  for i := 0 to GetArrayLength(NewLines) - 1 do
  begin
    Trimmed := TrimStr(NewLines[i]);
    if (Trimmed = '') or (Copy(Trimmed, 1, 1) <> '"') then
      Continue; { salta la apertura, el cierre y las líneas en blanco — solo pares clave/valor }
    quotePos := Pos('"', Copy(Trimmed, 2, MaxInt));
    if quotePos = 0 then
      Continue;
    KeyName := Copy(Trimmed, 2, quotePos - 1);
    if ConfigHasKey(ExistingLines, KeyName) then
      Continue;
    { Normalizar ACÁ, no al escribir (Citrino, bug real encontrado antes
      de integrar): las líneas de la plantilla vienen con coma final
      salvo la última (ver cómo arma ConfigJSON, más abajo) — guardar esa
      coma en ToAdd y agregar otra según la posición al escribir producía
      doble coma, o coma colgante antes del cierre si la única clave
      faltante (addCount=1) entraba por la rama "no es la última" con su
      coma original todavía puesta. ToAdd guarda pares SIN coma — la
      lógica de "coma en todos menos el último", más abajo, pasa a ser la
      única fuente de comas y queda correcta tal como está escrita. }
    if Copy(Trimmed, Length(Trimmed), 1) = ',' then
      Trimmed := Copy(Trimmed, 1, Length(Trimmed) - 1);
    SetArrayLength(ToAdd, addCount + 1);
    ToAdd[addCount] := Trimmed;
    addCount := addCount + 1;
  end;

  if addCount = 0 then
  begin
    Result := ExistingRaw; { ya tiene todo — no tocar ni un byte }
    Exit;
  end;

  { Ubicar la línea de cierre — la última línea no vacía del archivo. }
  closeIdx := -1;
  for i := GetArrayLength(ExistingLines) - 1 downto 0 do
  begin
    if TrimStr(ExistingLines[i]) <> '' then
    begin
      closeIdx := i;
      Break;
    end;
  end;
  { closeIdx < 2, no < 1 (R10, hallazgo de Peridot): con closeIdx = 1 el
    archivo es solo la línea de apertura y la de cierre, sin un solo par de
    clave/valor. La reconstrucción de abajo le agrega una coma a la línea
    anterior al cierre — que en ese caso es la apertura misma — y produce
    JSON inválido. Hoy no es alcanzable porque FinalizeKeys ya abortó si
    faltan las 3 claves base, pero eso es una dependencia implícita entre
    dos hooks: el día que alguien reordene, se rompe en silencio. Un guard
    no debe apoyarse en lo que hizo otro paso.
    Ojo al editar este comentario: una llave de cierre suelta acá adentro
    lo termina antes de tiempo y rompe la compilación del script. }
  if (closeIdx < 2) or (Copy(TrimStr(ExistingLines[closeIdx]), 1, 1) <> '}') then
  begin
    { Formato irreconocible — no debería pasar nunca (ResolveKeys ya
      validó que las 3 claves base se pudieron leer de este mismo
      archivo), pero si pasa, no arriesgar un archivo corrupto: devolver
      tal cual, sin completar. }
    Result := ExistingRaw;
    Exit;
  end;

  { Reconstruir: todo lo anterior a la última línea de contenido, sin
    tocar una letra; la última línea de contenido, con coma agregada si
    le faltaba (JSON válido antes de sumar más pares); las claves nuevas,
    coma en todas menos la última; el cierre, igual que estaba. }
  Output := '';
  for i := 0 to closeIdx - 2 do
    Output := Output + ExistingLines[i] + #13#10;

  LastContentLine := ExistingLines[closeIdx - 1];
  Trimmed := TrimStr(LastContentLine);
  if (Trimmed <> '') and (Copy(Trimmed, Length(Trimmed), 1) <> ',') then
    LastContentLine := LastContentLine + ',';
  Output := Output + LastContentLine + #13#10;

  for i := 0 to addCount - 1 do
  begin
    if i < addCount - 1 then
      Output := Output + '  ' + ToAdd[i] + ',' + #13#10
    else
      Output := Output + '  ' + ToAdd[i] + #13#10;
  end;
  Output := Output + ExistingLines[closeIdx] + #13#10;

  Result := Output;
end;

{ FinalizeKeys: la validación COMPARTIDA entre las dos fuentes que
  ResolveKeys puede leer (piumy-config.json o un run-piumy.bat viejo) —
  exige que las 3 claves base (MCP/REST/BACKUP, las que TODA versión
  publicada tuvo) estén las tres; si falta cualquiera, no cierra (dañado,
  truncado, o el formato no matcheó nada) y aborta SIN completar lo que
  falta. T28 (ct-2026-08-05-2242): ya no hay una 4ta clave que generar
  como excepción — CapiKey/PIUMY_CAPI_KEY se sacó, el despacho no la
  necesita más. }
function FinalizeKeys(const SourceLabel: String;
  HasMcp, HasRest, HasBackup: Boolean): String;
begin
  Result := '';
  if not (HasMcp and HasRest and HasBackup) then
  begin
    Log('Piumy: ' + SourceLabel + ' existente — claves encontradas: MCP=' + BoolStr(HasMcp) +
      ' REST=' + BoolStr(HasRest) + ' BACKUP=' + BoolStr(HasBackup));
    Result := 'Piumy: ya existe una instalación (' + SourceLabel + ') pero no se pudieron reconocer todas sus claves — el archivo puede estar dañado o truncado. La instalación se detiene para no generar claves nuevas que reemplacen las que ya protegen sus backups. Revise el archivo a mano, o si de verdad quiere claves nuevas, borre ' + SourceLabel + ' antes de reinstalar.';
    Exit;
  end;
  Log('Piumy: PIUMY_MCP_KEY reusada de ' + SourceLabel + '.');
  Log('Piumy: PIUMY_REST_KEY reusada de ' + SourceLabel + '.');
  Log('Piumy: PIUMY_BACKUP_KEY reusada de ' + SourceLabel + '.');
end;

{ ResolveKeys: única fuente de las 4 claves para toda la instalación
  (nueva o actualización) — T11 (ct-2026-08-05-1214) le agregó una tercera
  fuente por encima de las 2 que ya tenía T6/T21/T22: primero
  piumy-config.json (el archivo de entrada nuevo — una instalación que ya
  actualizó a T11), si no existe entonces run-piumy.bat (una instalación
  vieja, sin actualizar — MISMA lógica de lectura tolerante que T21/T22
  escribieron, sin tocar), y si ninguno existe, instalación limpia: genera
  las 3 (T28, ct-2026-08-05-2242: eran 4, CapiKey se sacó — el despacho
  no la necesita más). Ambas fuentes existentes comparten FinalizeKeys
  (arriba) para la validación — no hay dos criterios de "¿esto cierra?"
  por mantener en sync. Devuelve '' si pudo resolver todo (+ RestAddr
  opcional) — el mensaje de error si no.

  NO usa RaiseException: eso corre bien desde NextButtonClick (T8), pero
  llamado desde ssPostInstall (adentro de CurStepChanged, DESPUÉS de que
  Inno ya copió los archivos y logueó "Installation process succeeded") NO
  vuelve un exit code distinto de cero — probado en carne propia (T21): el
  instalador terminaba con exit 0 pese al abort, y con /SUPPRESSMSGBOXES
  roto por un bug de mangling de paths de Git Bash en las pruebas, el
  diálogo de error que Inno sí muestra internamente se quedó esperando un
  click que nunca llega — una instalación desatendida real se cuelga para
  siempre. Por eso esta función es pura (sin side effects más que Log) y
  quien la llama es PrepareToInstall (ver abajo): ese hook corre ANTES de
  copiar un solo archivo, y devolver un string no vacío ahí es el
  mecanismo QUE INNO RECONOCE como fallo real — cancela la instalación
  entera (nada queda a medio escribir) y el exit code SÍ sale distinto de
  cero. }
function ResolveKeys(const ConfigPath, BatPath: String;
  var McpKey, RestKey, BackupKey, RestAddr: String): String;
var
  RawBytes: AnsiString;
  Content: String;
  Lines: TArrayOfString;
  i: Integer;
  V: String;
  HasMcp, HasRest, HasBackup: Boolean;
begin
  Result := '';
  RestAddr := ''; { '' = sin override — usar el default de config.go (":8092") }

  if FileExists(ConfigPath) then
  begin
    if not LoadStringFromFile(ConfigPath, RawBytes) then
    begin
      Result := 'Piumy: ya existe una instalación (piumy-config.json) pero no se pudo leer ese archivo — ¿está bloqueado por otro programa, o sin permisos? La instalación se detiene para no arriesgar las claves existentes. Cierre lo que lo esté usando y vuelva a intentar.';
      Exit;
    end;
    if HasBOM(RawBytes) then
    begin
      Result := 'Piumy: piumy-config.json existe pero está guardado en un formato distinto al esperado (Unicode/UTF-16, o UTF-8 con marca de orden de bytes). La instalación se detiene para no generar claves nuevas que reemplacen las que ya protegen sus backups. Vuelva a guardarlo en UTF-8/ANSI, o si de verdad quiere claves nuevas, borre piumy-config.json antes de reinstalar.';
      Exit;
    end;
    Content := RawBytes;
    Lines := SplitLines(Content);
    McpKey := ReadConfigJSONValue(Lines, 'PIUMY_MCP_KEY');
    RestKey := ReadConfigJSONValue(Lines, 'PIUMY_REST_KEY');
    BackupKey := ReadConfigJSONValue(Lines, 'PIUMY_BACKUP_KEY');
    RestAddr := ReadConfigJSONValue(Lines, 'PIUMY_REST_ADDR');
    Result := FinalizeKeys('piumy-config.json', McpKey <> '', RestKey <> '', BackupKey <> '');
    Exit;
  end;

  if FileExists(BatPath) then
  begin
    if not LoadStringFromFile(BatPath, RawBytes) then
    begin
      Result := 'Piumy: ya existe una instalación (run-piumy.bat) pero no se pudo leer ese archivo — ¿está bloqueado por otro programa, o sin permisos? La instalación se detiene para no arriesgar las claves existentes. Cierre lo que lo esté usando y vuelva a intentar.';
      Exit;
    end;
    if HasBOM(RawBytes) then
    begin
      Result := 'Piumy: run-piumy.bat existe pero está guardado en un formato distinto al esperado (Unicode/UTF-16, o UTF-8 con marca de orden de bytes) — esto pasa si se lo abrió en el Bloc de notas y se guardó eligiendo "Unicode" en vez de "ANSI". La instalación se detiene para no generar claves nuevas que reemplacen las que ya protegen sus backups. Vuelva a guardar el archivo como texto ANSI, o si de verdad quiere claves nuevas, borre run-piumy.bat antes de reinstalar.';
      Exit;
    end;

    Content := RawBytes;
    Lines := SplitLines(Content);
    HasMcp := False;
    HasRest := False;
    HasBackup := False;
    for i := 0 to GetArrayLength(Lines) - 1 do
    begin
      if ParseSetLine(Lines[i], 'PIUMY_MCP_KEY', V) then
      begin
        McpKey := V;
        HasMcp := True;
      end;
      if ParseSetLine(Lines[i], 'PIUMY_REST_KEY', V) then
      begin
        RestKey := V;
        HasRest := True;
      end;
      if ParseSetLine(Lines[i], 'PIUMY_BACKUP_KEY', V) then
      begin
        BackupKey := V;
        HasBackup := True;
      end;
      { PIUMY_REST_ADDR (T21, secundario, Citrino): no es una clave — no
        participa de FinalizeKeys ni tiene excepción legítima que
        distinguir. Si alguien lo agregó a mano (para esquivar un choque
        de puerto), se preserva; si no está, RestAddr queda '' y
        piumy-config.json no escribe la línea — mismo puerto por defecto
        de siempre. }
      if ParseSetLine(Lines[i], 'PIUMY_REST_ADDR', V) then
        RestAddr := V;
    end;

    Result := FinalizeKeys('run-piumy.bat', HasMcp, HasRest, HasBackup);
    Exit;
  end;

  { instalación limpia — ni piumy-config.json ni run-piumy.bat: generar
    las 3. }
  Result := TryGenerateRandomHex(16, McpKey);
  if Result <> '' then Exit;
  Result := TryGenerateRandomHex(24, RestKey);
  if Result <> '' then Exit;
  Result := TryGenerateRandomHex(32, BackupKey);
  if Result <> '' then Exit;
  Log('Piumy: instalación nueva — PIUMY_MCP_KEY/PIUMY_REST_KEY/PIUMY_BACKUP_KEY generadas.');
end;

{ ── T34 (ct-2026-08-07, bug reportado por el boss con captura): el diálogo
    "Piumy está ejecutándose, ciérrelo y hacé clic en Aceptar" se quedaba
    esperando a un usuario que a veces ni sabe cómo cerrar una app de
    bandeja sin ventana — y en modo desatendido (/VERYSILENT
    /SUPPRESSMSGBOXES) el diálogo suprimido respondía Cancelar por
    defecto: exit code 1, pero SIN instalar nada — ya publicado en 0.1.6.

    Causa: `AppMutex` (arriba) solo hace que Inno MUESTRE ese diálogo — no
    cierra nada solo. `CloseApplications=yes` (RestartManager) es la vía
    oficial de Inno para cerrar solo, pero se sospechaba que no detecta un
    proceso de bandeja sin ventana principal — confirmado con un decoy
    `-H windowsgui` que solo toma un mutex y duerme (T34, sin código de
    producto real de por medio): con SOLO `CloseApplications=yes` puesto,
    el decoy queda corriendo y el diálogo de todos modos aparece. La salida
    es un cierre EXPLÍCITO, no delegado a RestartManager. }
function IsImageRunning(const ImageName: String): Boolean;
var
  ResultCode: Integer;
  OutFile: String;
  Raw: AnsiString;
begin
  { tasklist con filtro, redirigido a un archivo temporal — Exec no expone
    stdout directo en Pascal Script, así que se relee del disco. Se busca
    el NOMBRE DE IMAGEN en la salida cruda (case-insensitive): si aparece,
    sigue corriendo. No se intenta parsear el mensaje "no hay tareas" —
    viene en el idioma de Windows, frágil; buscar la presencia del nombre
    no depende de ningún idioma. }
  Result := False;
  OutFile := ExpandConstant('{tmp}\piumy-tasklist.txt');
  if Exec(ExpandConstant('{cmd}'),
     '/C tasklist.exe /FI "IMAGENAME eq ' + ImageName + '" /NH > "' + OutFile + '" 2>&1',
     '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    if FileExists(OutFile) and LoadStringFromFile(OutFile, Raw) then
      Result := Pos(Lowercase(ImageName), Lowercase(String(Raw))) > 0;
    DeleteFile(OutFile);
  end;
end;

{ CloseRunningInstance: mata ImageName por la fuerza (taskkill /F) y
  VERIFICA que de verdad desapareció — no confía en el exit code de
  taskkill (varía entre versiones de Windows), confía en volver a mirar
  con tasklist. /F es TerminateProcess, casi instantáneo, pero el handle
  del archivo puede tardar un instante en soltarse — de ahí el reintento
  corto (hasta 5s) antes de darse por vencido. Si sigue detectándose
  corriendo, devuelve un mensaje de error (no vacío) — el llamador
  (InitializeSetup) lo propaga como Result := False, que es lo que hace
  que el modo silencioso falle con exit code distinto de cero en vez de
  proceder como si nada. }
function CloseRunningInstance(const ImageName: String): String;
var
  ResultCode: Integer;
  Attempt: Integer;
begin
  Result := '';
  if not IsImageRunning(ImageName) then
    Exit; { caso común: no había nada corriendo }
  Exec('taskkill.exe', '/F /IM "' + ImageName + '"', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode);
  for Attempt := 1 to 10 do
  begin
    if not IsImageRunning(ImageName) then
      Exit;
    Sleep(500);
  end;
  Result := 'Piumy: había una instalación de Piumy corriendo (' + ImageName + ') y no se pudo cerrar automáticamente. Ciérrela a mano (ícono junto al reloj, clic derecho, Salir) y vuelva a ejecutar el instalador.';
end;

{ PrepareToInstall corre DESPUÉS de "Listo para instalar" (el usuario ya
  confirmó la carpeta de instalación) pero ANTES de copiar un solo archivo
  — el momento correcto para poder abortar limpio: devolver un string no
  vacío cancela TODA la instalación (Inno lo trata como fallo real, exit
  code distinto de cero, sin dejar nada a medio escribir) y corre
  IGUAL en modo silencioso (WizardSilent no lo salta). Guarda el
  resultado en las globales Resolved* — CurStepChanged (ssPostInstall)
  las usa tal cual, sin volver a leer ni poder abortar ahí.

  T34: el cierre de la instancia corriendo NO va acá — ver InitializeSetup
  más abajo. Se probó primero acá (boss verbatim: "que el instalador lo
  haga en el primer paso", y esta función es la que ya usa el patrón
  "string no vacío = aborta limpio") pero un log real (/LOG=, decoy T34)
  mostró que Inno nunca llega a llamar a PrepareToInstall: su propio
  diálogo de AppMutex corre ANTES, dispara un EAbort y aborta la
  instalación entera sin que ninguna línea de este archivo se ejecute —
  "Defaulting to Cancel for suppressed message box" en el log, cero
  llamadas a Log() propias. PrepareToInstall es DEMASIADO TARDE para esto;
  InitializeSetup corre antes que cualquier chequeo interno de Inno. }
function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := ResolveKeys(ExpandConstant('{app}\piumy-config.json'), ExpandConstant('{app}\run-piumy.bat'),
    ResolvedMcpKey, ResolvedRestKey, ResolvedBackupKey, ResolvedRestAddr);
end;

{ ── T34: InitializeSetup es el primer código de este script que corre —
  antes de que Inno haga NINGUNO de sus propios chequeos internos
  (AppMutex incluido). Cerrar acá, sin preguntar, es lo único que logra
  que el diálogo de AppMutex nunca llegue a dispararse — confirmado con
  log real contra un decoy (ver el comentario de PrepareToInstall arriba
  para el porqué se movió). Devolver False acá cancela TODO el setup,
  ANTES de mostrar una sola página del wizard — mismo mecanismo de "string
  no vacío"/"False = aborta" que ya usa PrepareToInstall, pero un paso
  antes en el ciclo de vida. }
function InitializeSetup(): Boolean;
var
  CloseErr: String;
begin
  CloseErr := CloseRunningInstance('{#MyAppExeName}');
  Result := CloseErr = '';
  if not Result then
  begin
    Log('Piumy: ' + CloseErr);
    if not WizardSilent then
      MsgBox(CloseErr, mbError, MB_OK);
  end;
end;

{ ── T8 (ct-2026-08-05-1133): un solo defecto con dos síntomas — la página
    de la clave solo contemplaba "un humano instalando por primera vez".
    ExistingInstallDetected reusa la misma detección que ResolveKeys usa
    para elegir fuente (T11: piumy-config.json O run-piumy.bat, cualquiera
    de los dos) para saltear la página entera al actualizar: no hay clave
    que pedir, la que hay se conserva (passHash es seed-only, T6). }
function ExistingInstallDetected: Boolean;
begin
  Result := FileExists(ExpandConstant('{app}\piumy-config.json')) or
    FileExists(ExpandConstant('{app}\run-piumy.bat'));
end;

{ ── T24 (ct-2026-08-05-1740, CRÍTICO — bloqueaba publicar): ExistingInstallDetected
    (arriba) confía en la constante de directorio de instalación que expone
    Inno, y esa la resuelve Inno mismo ANTES de que corra una sola línea
    nuestra — vía el AppId en el registro (HKCU...Uninstall\...) si lo
    reconoce como actualización, o el directorio por defecto (DefaultDirName)
    si no. Si esa entrada de registro falta o quedó desactualizada
    (instalación hecha a mano, limpieza de registro, carpeta movida —
    cualquier motivo, no hace falta saber cuál) el directorio de instalación
    cae al default de siempre — y como DefaultDirName nunca cambió en
    ninguna versión publicada, chequear esa misma carpeta otra vez no agrega
    nada: es exactamente donde ya cayó. Reproducido en aislamiento (T24, con
    un default de prueba distinto del real para poder aislar el caso sin
    tocar una instalación real de esta máquina): sin entrada de registro, el
    directorio de instalación cae al default aunque el run-piumy.bat real
    esté en OTRA carpeta — pide la clave del tablero de nuevo y genera 4
    claves nuevas sobre una instalación que ya tenía las suyas. Mismo
    desastre que T21 cerró para "el registro reconoce, pero el archivo no se
    puede leer con certeza"; a este otro caso ("el registro no reconoce
    nada, la instalación real está en otra carpeta") no lo cubría nada
    todavía.

    La única fuente que sobrevive a que el registro se pierda es el acceso
    directo del menú inicio: la sección Icons lo crea SIEMPRE (sin Tasks:, a
    diferencia del de inicio automático) apuntando directo al ejecutable
    dentro de la carpeta real de instalación, y para que ese acceso directo
    alguna vez haya arrancado la app de verdad, tuvo que apuntar a la
    carpeta REAL — no a una que Inno "cree" que es la correcta. Leerlo con
    WScript.Shell (mismo mecanismo que T11 ya usó para VERIFICAR el .lnk
    real, ahora para LEERLO en runtime) recupera esa carpeta sin depender
    del registro.

    ADVERTENCIA para quien construya el instalador de Linux/Mac (post-MVP,
    ver CLAUDE.md): esta función (WScript.Shell, un .lnk de Windows) es
    Windows puro y NO viaja — nada de esto existe fuera de Windows. Lo que
    SÍ cruza es el PROBLEMA que resuelve: la carpeta que el instalador
    precarga por su cuenta puede no ser la instalación real cuando el
    mecanismo del sistema para reconocer una instalación previa falta o
    quedó desactualizado — y de ahí sale pedir credenciales de nuevo y
    generar claves nuevas sobre una instalación que ya tenía las suyas. En
    Linux/Mac ese mecanismo del sistema no es un registro de Windows, así
    que el defecto que lo dispara es distinto — y probablemente el fix sea
    más simple ahí: un archivo de configuración en la ruta estándar de esa
    plataforma (ver piumy-config.json, T11) alcanza para saber si ya hay
    una instalación, sin necesitar un acceso directo. Pero esa decisión es
    de quien construya ese instalador, no de este archivo: portar ESTA
    función (buscar un .lnk que ahí no existe) sería resolver el problema
    equivocado. }
function PreviousInstallDirFromShortcut: String;
var
  WshShell, Shortcut: Variant;
  TargetPath: String;
begin
  Result := '';
  if not FileExists(ExpandConstant('{autoprograms}\{#MyAppName}.lnk')) then
    Exit;
  try
    WshShell := CreateOleObject('WScript.Shell');
    Shortcut := WshShell.CreateShortcut(ExpandConstant('{autoprograms}\{#MyAppName}.lnk'));
    TargetPath := Shortcut.TargetPath;
  except
    Exit;
  end;
  if TargetPath <> '' then
    Result := ExtractFileDir(TargetPath);
end;

function DirHasInstall(const Dir: String): Boolean;
begin
  Result := (Dir <> '') and
    (FileExists(Dir + '\piumy-config.json') or FileExists(Dir + '\run-piumy.bat'));
end;

{ ── Página propia: la clave del tablero + el correo de recuperación
    (ct-2026-07-31-1643) — se piden ACÁ, durante la instalación, para que
    nunca exista una instalación nueva con la clave de fábrica admin/piumy,
    y para que el primer arranque ya tenga un camino de recuperación
    configurado si el usuario lo da. Correo, misma página que la clave (es
    el mismo tema) — no una página aparte. }
procedure InitializeWizard;
var
  ShortcutDir: String;
begin
  { T24: ver comentario de PreviousInstallDirFromShortcut arriba. Ojo:
    ExpandConstant de la constante de directorio de instalación NO se puede
    llamar todavía acá — Inno tira "Error interno: se intentó expandir la
    constante antes de que se inicialice" (probado en carne propia). Por
    eso se chequea WizardForm.DirEdit.Text directo, como String — Inno ya lo
    precargó (vía registro, o el default si no) ANTES de llamar a este
    procedimiento, aunque la constante en sí todavía no se pueda expandir.
    Si esa precarga ya apunta a una carpeta con instalación, no se toca
    nada — esa fuente es igual de válida y no hace falta reemplazarla.
    Forzar el campo ACÁ, antes de que se cree una sola página, alcanza: el
    directorio de instalación de ahí en más (ShouldSkipPage,
    PrepareToInstall, ResolveKeys, todos vía ExpandConstant, ya inicializada
    para entonces) lee el mismo WizardForm.DirEdit.Text. }
  if not DirHasInstall(WizardForm.DirEdit.Text) then
  begin
    ShortcutDir := PreviousInstallDirFromShortcut;
    if DirHasInstall(ShortcutDir) then
      WizardForm.DirEdit.Text := ShortcutDir;
  end;

  { Texto de la bienvenida, verbatim del boss — no reescribir. }
  WizardForm.WelcomeLabel1.Caption := 'Piumy atiende tu WhatsApp con agentes de inteligencia artificial.';
  WizardForm.WelcomeLabel2.Caption :=
    'Tú decides, conversación por conversación, cuánta libertad les das: desde que respondan solos hasta que nada salga sin que lo leas antes.' + #13#10 + #13#10 +
    'Todo funciona en este computador. No hay servidores de por medio.';

  PasswordPage := CreateInputQueryPage(wpSelectDir,
    'Clave del tablero',
    'Elija la clave para administrar Piumy',
    'Con esta clave va a entrar al tablero (reglas, mensajes, quién es el dueño). Se pide una sola vez, ahora — después se cambia desde el propio tablero.' + #13#10 + #13#10 +
    'El código de recuperación llega a su WhatsApp. Si además configura el envío de correos desde el tablero, también le llega por correo — el campo de abajo es opcional, puede dejarlo vacío y seguir.');
  PasswordPage.Add('Clave:', True);
  PasswordPage.Add('Repita la clave:', True);
  PasswordPage.Add('Correo de recuperación de contraseña:', False);
end;

{ ── T8: instalación existente → la página no aparece (nada que pedir).
    Verificado (arnés de prueba aparte, borrado): ShouldSkipPage corre
    DESPUÉS de wpSelectDir, así que la carpeta de instalación ya refleja
    el directorio real elegido (default o editado), no el de fábrica. }
function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := False;
  if PageID = PasswordPage.ID then
    Result := ExistingInstallDetected;
end;

{ ── T8: modo silencioso — nadie ve esta página, así que NextButtonClick
    no puede validar Values[] (llegan vacíos, nunca porque el usuario los
    dejó vacíos). La clave sale de /DASHBOARDPASSWORD; si falta, decisión
    del boss (verbatim: "que por defecto la clave del instalador sea
    piumy si se hace instalacion silenciosa"): NO aborta, cae al mismo
    default de fábrica que ya usa auth.go (dashboardDefaultPassword =
    "piumy") si nunca hay hash guardado — y lo deja BIEN VISIBLE en el
    log de instalación (Log, no un MsgBox que en modo silencioso nadie
    lee), para quien despliegue muchas máquinas desatendidas pueda
    encontrar cuáles quedaron con la de fábrica. Precedencia: parámetro
    primero, "piumy" solo como último recurso — nunca al revés. Un
    parámetro PRESENTE pero inválido (< 4 caracteres) sí aborta: ahí no es
    "no me dieron nada", es "me dieron algo mal" — RaiseException, no
    MsgBox (verificado con el arnés de prueba: exit code 1 distinguible de
    éxito, y el mensaje SÍ queda en el log aun con /SUPPRESSMSGBOXES). }
function NextButtonClick(CurPageID: Integer): Boolean;
var
  SilentPassword: String;
begin
  Result := True;
  if CurPageID <> PasswordPage.ID then
    Exit;

  if WizardSilent then
  begin
    SilentPassword := ExpandConstant('{param:DASHBOARDPASSWORD|}');
    if SilentPassword = '' then
    begin
      Log('Piumy: instalación silenciosa sin /DASHBOARDPASSWORD — queda con la clave de fábrica (admin/piumy). Cámbiela desde el tablero apenas pueda.');
      SilentPassword := 'piumy';
    end
    else if Length(SilentPassword) < 4 then
    begin
      RaiseException('Piumy: /DASHBOARDPASSWORD tiene que tener al menos 4 caracteres.');
    end;
    PasswordPage.Values[0] := SilentPassword;
    PasswordPage.Values[1] := SilentPassword;
    PasswordPage.Values[2] := ExpandConstant('{param:RECOVERYEMAIL|}');
    Exit;
  end;

  if Length(PasswordPage.Values[0]) < 4 then
  begin
    MsgBox('La clave tiene que tener al menos 4 caracteres.', mbError, MB_OK);
    Result := False;
  end
  else if PasswordPage.Values[0] <> PasswordPage.Values[1] then
  begin
    MsgBox('Las dos claves que escribió no coinciden.', mbError, MB_OK);
    Result := False;
  end;
end;

{ ── Post-instalación: lanza el programa UNA vez con las 9 variables (ya
    persistidas en piumy-config.json por CurStepChanged, T11) + la clave
    del tablero + el correo de recuperación puestos en el entorno de ESTE
    proceso (que el hijo hereda y nadie más lee — nunca quedan escritos en
    disco), y abre el tablero en el navegador.

    RestAddr (T21, ct-2026-08-05-1308, secundario, agregado de Citrino):
    '' = puerto por defecto de config.go (8092); si no, el valor que
    ResolveKeys preservó de una instalación existente (alguien agregó
    PIUMY_REST_ADDR a mano, típicamente para esquivar un choque de
    puerto) — abrir el 8092 fijo en ese caso le mostraba al usuario el
    tablero de OTRA instalación, o pegaba en la nada si el 8092 estaba
    libre pero ocupado por otra cosa. }
procedure LaunchFirstRunAndOpenDashboard(McpKey, RestKey, BackupKey, RecoveryEmail, RestAddr: String);
var
  ResultCode: Integer;
  DashboardPort: String;
begin
  SetEnvironmentVariableW('PIUMY_DB_PATH', ExpandConstant('{app}\secrets\piumy.db'));
  SetEnvironmentVariableW('PIUMY_WA_DB_PATH', ExpandConstant('{app}\secrets\whatsmeow.db'));
  SetEnvironmentVariableW('PIUMY_ROUTER_PATH', ExpandConstant('{app}\secrets\router.json'));
  SetEnvironmentVariableW('PIUMY_STATUS_PATH', ExpandConstant('{app}\secrets\status.json'));
  SetEnvironmentVariableW('PIUMY_MEDIA_DIR', ExpandConstant('{app}\secrets\media'));
  SetEnvironmentVariableW('PIUMY_MCP_KEY', McpKey);
  SetEnvironmentVariableW('PIUMY_REST_KEY', RestKey);
  SetEnvironmentVariableW('PIUMY_BACKUP_DIR', ExpandConstant('{app}\secrets\backups'));
  SetEnvironmentVariableW('PIUMY_BACKUP_KEY', BackupKey);
  if RestAddr <> '' then
    SetEnvironmentVariableW('PIUMY_REST_ADDR', RestAddr);
  { Solo esta vez, solo en este proceso — ver auth.go: passHash() la lee
    nada más que cuando todavía no hay hash guardado. }
  SetEnvironmentVariableW('PIUMY_DASHBOARD_PASSWORD', PasswordPage.Values[0]);
  { Ídem, admin.go: SeedRecoveryEmailFromEnv() la lee una sola vez al
    arrancar, solo si el KV está vacío. RecoveryEmail puede venir vacío
    (campo opcional) — el gateway lo maneja sin problema (no-op). }
  SetEnvironmentVariableW('PIUMY_DASHBOARD_RECOVERY_EMAIL', RecoveryEmail);

  { T23 (ct-2026-08-05-1615, R4 de Amatista): mismo patrón que la línea
    ~740 (fallo al escribir el launcher) — un antivirus o SmartScreen
    bloqueando el arranque de un ejecutable nuevo sin firma es igual o más
    probable que bloqueando la creación del .bat. Las claves ya están
    escritas en run-piumy.bat en este punto (estado recuperable — un
    acceso directo alcanza), pero el MsgBox incondicional de antes colgaba
    una instalación desatendida para siempre (mismo bug que T22 encontró
    y arregló en la escritura del launcher, vivo acá también). Log
    siempre; MsgBox solo si hay alguien mirando. }
  if not Exec(ExpandConstant('{app}\{#MyAppExeName}'), '', ExpandConstant('{app}'),
     SW_HIDE, ewNoWait, ResultCode) then
  begin
    Log('Piumy: no se pudo arrancar Piumy.exe automáticamente tras instalar — probablemente un antivirus o SmartScreen bloqueando el ejecutable. La configuración y las claves ya quedaron guardadas en piumy-config.json; abra la app desde el acceso directo cuando pueda.');
    if not WizardSilent then
      MsgBox('Piumy se instaló, pero no pude arrancarlo automáticamente. Ábralo desde el acceso directo.', mbError, MB_OK);
  end;

  { Limpieza defensiva — el hijo ya se llevó su propia copia del entorno
    al arrancar; esto es cinturón y tirantes, no lo esencial. }
  SetEnvironmentVariableW('PIUMY_DASHBOARD_PASSWORD', '');
  SetEnvironmentVariableW('PIUMY_DASHBOARD_RECOVERY_EMAIL', '');

  { T21: una instalación desatendida no tiene a nadie mirando la pantalla
    — abrirle un navegador a un usuario que instala en varias máquinas es
    ruido en el mejor caso, y en el peor le pisa una pestaña que ya tenía
    abierta (el caso real que motivó esto: el boss vio pestañas del
    tablero abrirse solas durante instalaciones de prueba silenciosas). }
  if WizardSilent then
    Exit;

  { El servidor tarda un instante en levantar el puerto — esperar antes
    de abrir el navegador, si no pega en un "conexión rechazada". }
  Sleep(3000);
  { T22 (ct-2026-08-05-1455, menor 2): PIUMY_REST_ADDR (net/http
    ListenAndServe) admite dos formas — ":PUERTO" (todas las interfaces,
    lo único que este instalador genera) o "host:puerto" (interfaz
    específica, si alguien lo escribió a mano). La versión vieja SIEMPRE
    anteponía "127.0.0.1" al valor preservado — con la segunda forma
    (ej. "127.0.0.1:9000") armaba "http://127.0.0.1127.0.0.1:9000",
    malformado. Cosmético (la app arranca bien igual, el launcher persiste
    el valor tal cual) pero se arregla en una línea. }
  if RestAddr = '' then
    DashboardPort := '127.0.0.1:8092' { default de config.go }
  else if RestAddr[1] = ':' then
    DashboardPort := '127.0.0.1' + RestAddr
  else
    DashboardPort := RestAddr; { ya trae su propio host }
  ShellExecAsOriginalUser('open', 'http://' + DashboardPort + '/dashboard/', '', '',
    SW_SHOWNORMAL, ewNoWait, ResultCode);
end;

{ ExeWasReplaced: T54 (ct-2026-08-10-1934) — el hallazgo de Citrino: si
  Piumy estaba corriendo, el instalador podía cerrar el proceso y de
  todos modos NO reemplazar Piumy.exe — sin ningún error. Verificado
  contra el código (CloseRunningInstance corre antes que cualquier otra
  cosa, el AppMutex coincide carácter por carácter con appmutex_windows.go)
  y contra una instalación descartable propia (el mecanismo real, con el
  binario real corriendo, no reprodujo el race en esta máquina — pero eso
  no importa: el código nunca verificaba el resultado de la copia, así
  que cualquier causa transitoria (antivirus, timing del sistema del
  usuario) podía colarse en silencio. Compara el SHA-256 del ejecutable
  YA INSTALADO contra el que trae este instalador ({#MyBuiltExeHash},
  calculado en tiempo de compilación) — no la versión (el binario no trae
  VersionInfo embebido) ni la fecha (reinstalar la misma build no la
  mueve). Envuelto en try/except: GetSHA256OfFile lanza una excepción si
  el archivo no se puede leer — ese caso también es "no coincide", nunca
  un crash sin explicación. }
function ExeWasReplaced(const ExePath: String): Boolean;
var
  InstalledHash: String;
begin
  Result := False;
  try
    InstalledHash := GetSHA256OfFile(ExePath);
    Result := InstalledHash = '{#MyBuiltExeHash}';
  except
    Log('Piumy: no se pudo calcular el hash de ' + ExePath + ' tras instalar: ' + GetExceptionMessage);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ConfigJSON: String;
  ConfigOK: Boolean;
  ConfigPath: String;
  ExistingConfigRaw: AnsiString;
  ExePath: String;
begin
  if CurStep = ssPostInstall then
  begin
    { T54: chequeo PRIMERO, antes de tocar config o cualquier otra cosa —
      si el ejecutable no se reemplazó, nada de lo que sigue tiene sentido
      hacer (mucho menos arrancar la versión vieja como si fuera la
      nueva). }
    ExePath := ExpandConstant('{app}\{#MyAppExeName}');
    if not ExeWasReplaced(ExePath) then
    begin
      Log('Piumy: el ejecutable instalado no coincide con el que trae este instalador (SHA-256 distinto) — probablemente algo lo tenía abierto o un antivirus lo retuvo durante la copia. La app NO se arranca, queda la versión anterior.');
      if not WizardSilent then
        MsgBox('Piumy no pudo actualizar su programa principal (Piumy.exe).' + #13#10 + #13#10 +
          'Probablemente estaba en uso, o un antivirus lo bloqueó durante la instalación — sigue corriendo la versión anterior.' + #13#10 + #13#10 +
          'Cierre Piumy por completo (ícono junto al reloj, clic derecho, Salir) y vuelva a ejecutar este instalador.', mbError, MB_OK);
      Exit;
    end;

    ForceDirectories(ExpandConstant('{app}\secrets\media'));
    ForceDirectories(ExpandConstant('{app}\secrets\backups'));

    { T21: las 3 claves (+ RestAddr opcional) ya están resueltas —
      PrepareToInstall corrió antes de copiar un solo archivo y hubiera
      abortado toda la instalación si no pudo resolverlas con certeza.
      Nada que leer ni que poder abortar acá. }

    { piumy-config.json PERMANENTE (T11, ct-2026-08-05-1214) — el archivo
      de ENTRADA que Piumy.exe lee al arrancar (config.ApplyFileDefaults,
      Go), reemplaza al run-piumy.bat que este mismo bloque escribía
      antes. NO contiene PIUMY_DASHBOARD_PASSWORD: esa variable solo
      existe en el entorno del proceso de instalación, una vez, más abajo
      — igual que el .bat viejo nunca la tuvo. Un solo par por línea, a
      propósito (JSONEscape/ReadConfigJSONValue arriba dependen de esta
      forma fija para poder releerlo en una reinstalación). }
    ConfigJSON :=
      '{' + #13#10 +
      '  "PIUMY_DB_PATH": "' + JSONEscape(ExpandConstant('{app}\secrets\piumy.db')) + '",' + #13#10 +
      '  "PIUMY_WA_DB_PATH": "' + JSONEscape(ExpandConstant('{app}\secrets\whatsmeow.db')) + '",' + #13#10 +
      '  "PIUMY_ROUTER_PATH": "' + JSONEscape(ExpandConstant('{app}\secrets\router.json')) + '",' + #13#10 +
      '  "PIUMY_STATUS_PATH": "' + JSONEscape(ExpandConstant('{app}\secrets\status.json')) + '",' + #13#10 +
      '  "PIUMY_MEDIA_DIR": "' + JSONEscape(ExpandConstant('{app}\secrets\media')) + '",' + #13#10 +
      '  "PIUMY_MCP_KEY": "' + JSONEscape(ResolvedMcpKey) + '",' + #13#10 +
      '  "PIUMY_REST_KEY": "' + JSONEscape(ResolvedRestKey) + '",' + #13#10 +
      '  "PIUMY_BACKUP_DIR": "' + JSONEscape(ExpandConstant('{app}\secrets\backups')) + '",' + #13#10 +
      '  "PIUMY_BACKUP_KEY": "' + JSONEscape(ResolvedBackupKey) + '"';
    if ResolvedRestAddr <> '' then
      { T21: preserva un puerto agregado a mano — si no, se perdía en
        silencio en cada reinstalación (la app volvía al 8092 default sin
        que nadie lo pidiera). }
      ConfigJSON := ConfigJSON + ',' + #13#10 + '  "PIUMY_REST_ADDR": "' + JSONEscape(ResolvedRestAddr) + '"';
    ConfigJSON := ConfigJSON + #13#10 + '}' + #13#10;

    { T51 (ct-2026-08-10-1826): si piumy-config.json YA existe, no
      reescribirlo desde la plantilla de arriba — eso descartaba
      cualquier clave que el usuario hubiera agregado a mano
      (PIUMY_DEFAULT_TERMINAL_ID es el caso real que lo hizo visible,
      perdido en vivo instalando 0.1.16). CompleteConfigJSON completa en
      vez de reemplazar: agrega solo lo que falte, conserva el resto
      textual. LoadStringFromFile puede fallar (permisos, antivirus) — en
      ese caso seguir con la plantilla de siempre, ResolveKeys ya validó
      antes que este mismo archivo se pudo leer una vez; no vale la pena
      abortar acá por una segunda lectura que falle. }
    ConfigPath := ExpandConstant('{app}\piumy-config.json');
    if FileExists(ConfigPath) and LoadStringFromFile(ConfigPath, ExistingConfigRaw) then
      ConfigJSON := CompleteConfigJSON(ExistingConfigRaw, ConfigJSON);

    { T22 (ct-2026-08-05-1455, R3 de Amatista, hueco 2): esta escritura no
      se chequeaba (entonces era run-piumy.bat; mismo riesgo, mismo
      arreglo, ahora en piumy-config.json). Un antivirus bloqueando la
      creación del archivo —actor real y frecuente, no hipotético— dejaba
      la instalación "exitosa" sin nada donde persistir las claves: solo
      vivían en el entorno de ESTE proceso (más abajo,
      LaunchFirstRunAndOpenDashboard) y se perdían al cerrarlo. Si esa
      corrida generaba un backup, quedaba cifrado con una clave que ya no
      existe en ningún lado — la misma pérdida de R2 (T21), entrando por
      la escritura en vez de la lectura. Ya estamos DESPUÉS de copiar los
      archivos (mismo punto tarde que ssPostInstall's propio
      RaiseException resultó ser para abortar — ver el doc de
      PrepareToInstall), así que acá no se aborta la instalación entera:
      se avisa honesto y se corta ANTES de arrancar la app, para que las
      claves generadas nunca lleguen a cifrar nada que después no se
      pueda leer. }
    ConfigOK := SaveStringToFile(ConfigPath, ConfigJSON, False);
    if not ConfigOK then
    begin
      Log('Piumy: no se pudo escribir piumy-config.json — probablemente un antivirus bloqueando la creación del archivo, o permisos. La app NO se arranca (evita cifrar un backup con claves que solo existen en este proceso y se pierden al cerrarlo).');
      { WizardSilent: un MsgBox() propio (a diferencia del diálogo interno
        de Inno que dispara un RaiseException no atrapado, sí cubierto por
        /SUPPRESSMSGBOXES — verificado en T21) NO se auto-responde con esa
        flag — probado en carne propia acá (T22): colgó el instalador
        para siempre en modo silencioso. Mismo patrón que NextButtonClick
        ya usa más abajo: MsgBox solo si hay alguien mirando; Log (ya
        emitido arriba) es lo único que sobrevive en modo silencioso — y
        es donde de verdad hace falta que sobreviva: un antivirus
        bloqueando el archivo es tan o más probable en un deploy
        desatendido que en uno interactivo. }
      if not WizardSilent then
        MsgBox('Piumy se instaló, pero no se pudo crear su archivo de configuración (piumy-config.json) — un antivirus puede estar bloqueándolo.' + #13#10 + #13#10 +
          'La aplicación NO se inició automáticamente a propósito, para no generar datos que después no se puedan leer.' + #13#10 + #13#10 +
          'Revise el antivirus y vuelva a ejecutar este instalador.', mbError, MB_OK);
      Exit;
    end;

    LaunchFirstRunAndOpenDashboard(ResolvedMcpKey, ResolvedRestKey, ResolvedBackupKey, PasswordPage.Values[2], ResolvedRestAddr);
  end;
end;

{ ── Desinstalación: avisa ANTES de borrar secrets/ — ahí vive el
    historial y las reglas del boss. No se lo lleva puesto en silencio. }
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  SecretsPath: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    SecretsPath := ExpandConstant('{app}\secrets');
    if DirExists(SecretsPath) then
    begin
      { T21 (ct-2026-08-05-1308, secundario): el propio texto aconseja
        elegir que no, pero el default marcaba "Sí" — quien viene
        apretando Enter (el gesto reflejo de cerrar diálogos) borraba su
        historial y su sesión sin querer. MB_DEFBUTTON2 pone el foco en
        "No", el mismo botón que el texto recomienda.

        T23 (ct-2026-08-05-1615, R4 de Amatista): este MsgBox también
        colgaba una desinstalación desatendida para siempre — mismo
        patrón que los otros dos de este archivo. Acá no hay forma de
        "avisar y seguir": es una pregunta Sí/No que decide si se borra
        algo irreversible, así que en silencioso no se pregunta —
        UninstallSilent salta directo al default que el propio texto ya
        recomienda (No, no borrar), igual que MB_DEFBUTTON2 hace para
        quien sí ve el diálogo. Un retiro desatendido de varias máquinas
        nunca pierde datos por un click que nadie dio. }
      if UninstallSilent then
        Log('Piumy: desinstalación silenciosa — los datos (' + SecretsPath + ') NO se borran (mismo default que el diálogo interactivo). Borre esa carpeta a mano si de verdad quiere borrar todo.')
      else if MsgBox('Piumy se va a desinstalar.' + #13#10 + #13#10 +
        '¿Borrar también los datos (' + SecretsPath + ')? Ahí vive el historial de mensajes, las reglas configuradas y la sesión de WhatsApp.' + #13#10 + #13#10 +
        'Si no está seguro, elija que NO — el programa se desinstala igual, y puede borrar esa carpeta a mano más adelante.',
        mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(SecretsPath, True, True, True);
    end;
  end;
end;
