// Nota histórica, no un test (T152, ct-2026-09-07-1917) — server.go tiene el
// comentario de paquete real, este archivo no compite con él.
//
// Este archivo tenía un TestSkillCopiesMatchSource: comparaba las 7 fuentes
// embebidas (operatorManual/orchestratorManual*/connectManual, server.go)
// contra sus copias bajo .claude/skills/ — la carpeta de la que CleverCoder
// lee las skills de un agente Claude Code (T74, ct-2026-08-27-1725). El
// motivo de T74 era bueno: que una copia desincronizada de su fuente FALLE
// RUIDOSAMENTE, no que dependa de que alguien se acuerde de copiarla a mano
// (la apuesta de "con una nota alcanza" ya se había perdido una vez).
//
// Se rompió CINCO veces en un día (T143, T148, T149, T151, y una vez del
// propio Citrino) — no por descuido de nadie, sino porque el mecanismo
// elegido no podía funcionar. Dos hallazgos, medidos, no asumidos:
//
//  1. **No hay UNA copia — hay una por instalación, y no las controla el
//     repo.** `.claude/skills/` no está trackeado por git (confirmado desde
//     T3: `git ls-files` no devuelve nada bajo `.claude/`, en ningún
//     checkout). Y mientras se investigaba este contrato se confirmó algo
//     más: esas copias se sincronizan EN VIVO durante la sesión —
//     `piumy-gateway/.claude/skills/piumy-operator/SKILL.md` (la raíz del
//     proyecto) y `coderoot/worktrees/tourmaline/.claude/skills/piumy-
//     operator/SKILL.md` (un worktree) son archivos DISTINTOS (inode
//     distinto, `fsutil reparsepoint query` descarta symlink/junction —
//     comprobado, no supuesto) que igual quedaron con contenido idéntico
//     minutos después de una edición. Es CleverCoder manteniéndolas al día
//     por su cuenta, no un archivo que este repo pueda prometer.
//  2. **El propio buscador de la copia (findSkillsCopyRoot, ya borrado de
//     este archivo) devolvía el PRIMER `.claude/skills/` que encontraba
//     caminando hacia arriba desde el cwd — sin verificar que tuviera los
//     archivos que iba a comparar.** Corrido desde distintos worktrees del
//     mismo repo, encontraba ubicaciones DISTINTAS: la copia recién
//     sincronizada de un worktree, una copia vieja sin ningún piumy-* en
//     otro, o directamente ninguna en un tercero. El test no era solo
//     inestable — su verde no significaba lo mismo dos veces según desde
//     dónde se corriera, así que no era una garantía débil: era una
//     garantía falsa.
//
// Combinado: el test vigilaba una ubicación que otro sistema ya mantiene al
// día por su cuenta, mirando además un lugar distinto cada vez que corría
// desde un worktree distinto. Sacarlo no deja un hueco de cobertura — saca
// una duplicación que además mentía sobre qué estaba cubriendo.
//
// Lo que NO se resuelve con un script de resincronización automática
// (descartado explícitamente en T152): sería automatizar la pelea contra un
// sistema que va a volver a pisar el archivo — más maquinaria sosteniendo el
// mismo malentendido.
//
// La garantía que queda, deliberadamente sin mecanismo — la disciplina que
// ya regía desde T148, ahora escrita donde alguien la va a encontrar: **si
// tocás algo en internal/mcpserver/manuals/, resincronizá tu copia local de
// .claude/skills/ antes de reportar verde.** No hay chequeo de código que lo
// exija — el código no puede prometer nada sobre un archivo que no controla.
//
// Si en algún momento se te ocurre "arreglar" esto trayendo de vuelta la
// comparación: no se perdió cobertura por descuido. Se sacó a propósito,
// porque comparar contra una ubicación no determinística es peor que no
// comparar — informa con apariencia de certeza algo que no sabe.
package mcpserver
