# runnerkit

[English](README.md) | Español

[![CI](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml/badge.svg)](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Ejecuta tu CI en tu propio ordenador en vez de gastar minutos de runners alojados. Funciona
con GitHub, Forgejo, Gitea y GitLab.

runnerkit instala el runner oficial de tu plataforma en Linux, macOS o Windows con un comando
y lo deja funcionando como servicio en segundo plano. Lo reinicia si falla, lo pausa si el
portátil va con batería, limpia las carpetas de compilación viejas y se niega a ejecutarse
como root. Tus workflows se quedan como están, salvo una línea.

```
runnerkit add mi-usuario/mi-repo-privado                    GitHub
runnerkit add https://git.ejemplo.com/mi-usuario/mi-repo    Forgejo, Gitea o GitLab
```

```
Runner portatil-mi-usuario-mi-repo-privado is installed and running.
To use it, put this in your workflow:

  runs-on: [self-hosted, linux, x64, runnerkit]
```

## Plataformas

| Plataforma | Runner que instala | Sistemas | Probado contra |
|---|---|---|---|
| GitHub | `actions/runner` | Linux, macOS, Windows | github.com, en los tres |
| Forgejo | `forgejo-runner` | Linux (el único que publica Forgejo) | Forgejo 16.0.7 |
| Gitea | `gitea-runner` | Linux, macOS, Windows | Gitea 1.27.3 |
| GitLab | `gitlab-runner` | Linux, macOS, Windows | GitLab CE 19.5.4 |

Gogs no tiene CI integrado, así que no hay runner que instalar. Para Forgejo en macOS o
Windows, compila el runner tú y pásalo con `--runner-binary`. Los detalles y las rarezas de
cada plataforma están en [docs/providers.md](docs/providers.md).

## Por qué

Los repos privados tienen una cantidad limitada de minutos gratis en los runners de GitHub,
y los minutos de macOS y Windows cuentan más que los de Linux. Los trabajos en un runner
propio no gastan esos minutos. Instalar el runner oficial no es difícil, pero dejarlo como
servicio en cada sistema, ordenado y seguro lleva su trabajo. runnerkit lo hace por ti.

A día de hoy GitHub no cobra los minutos de runners propios. Anunció una tarifa por minuto
en diciembre de 2025 y la aplazó; mira la página de facturación de GitHub antes de fiarte.

## Instalación

Descarga el binario de tu sistema desde la página de releases y ponlo en el PATH. Es un solo
fichero sin dependencias.

| Sistema | Fichero |
|---|---|
| Linux | `runnerkit-linux-amd64`, `runnerkit-linux-arm64` |
| macOS | `runnerkit-darwin-arm64`, `runnerkit-darwin-amd64` |
| Windows | `runnerkit-windows-amd64.exe`, `runnerkit-windows-arm64.exe` |

O compílalo con `make build` (Go 1.24 o superior).

## Uso

Primero comprueba el equipo:

```
runnerkit doctor
```

Hace falta un token de acceso que pueda gestionar los runners del repositorio. En GitHub lo
más fácil es el CLI: ejecuta `gh auth login` una vez y runnerkit recoge el token. runnerkit
lo busca en `--token`, luego `RUNNERKIT_TOKEN`, luego la variable de la plataforma
(`GH_TOKEN` o `GITHUB_TOKEN`, `FORGEJO_TOKEN`, `GITEA_TOKEN`, `GITLAB_TOKEN`) y, en GitHub,
`gh`. Solo se usa mientras corre el comando y runnerkit nunca lo escribe en disco. Los
permisos que pide cada plataforma están en [docs/providers.md](docs/providers.md).

```
runnerkit add usuario/repo                 un repositorio de GitHub
runnerkit add mi-org --group Default       una organización de GitHub, con grupo opcional
runnerkit add https://HOST/usuario/repo    un repositorio de Forgejo, Gitea o GitLab
runnerkit add usuario/repo --labels gpu    etiquetas extra
runnerkit list                             runners de este equipo y su estado
runnerkit status NOMBRE                    detalle de uno
runnerkit logs NOMBRE -n 100               final de su registro
runnerkit stop NOMBRE                      también start y restart; --force para con un trabajo en marcha
runnerkit clean --dry-run                  qué carpetas viejas se borrarían
runnerkit remove NOMBRE                    lo da de baja en GitHub y borra todo
```

Otras opciones de `add`: `--name`, `--no-start`, `--no-pause-on-battery`, `--allow-public`,
`--replace` (sustituye un runner del mismo nombre ya registrado en GitHub), `--forge` (si la
dirección no dice de qué plataforma es), `--runner-binary` y `--allow-http`.
`clean --days N` cambia la antigüedad mínima de una carpeta de trabajo. `remove --force`
borra en local cuando no se puede llegar a GitHub.

En Linux, ejecuta una vez `loginctl enable-linger $USER` para que el runner siga activo al
cerrar sesión. En macOS y Windows el runner arranca al iniciar sesión y se detiene al cerrarla.

## Qué hace

- Instala el runner oficial de la plataforma y comprueba antes su SHA-256; no continúa sin él.
- Lo ejecuta como servicio de usuario: systemd en Linux, launchd en macOS y el Programador de
  tareas en Windows. No pide permisos de administrador.
- Lo reinicia si falla, con una espera creciente.
- Lo pausa con batería cuando no hay ningún trabajo en marcha, y nunca interrumpe un trabajo por la batería.
- Lo ejecuta con prioridad baja para que el equipo siga siendo usable.
- Borra carpetas de trabajo y registros viejos.
- Mantiene tu token de GitHub fuera de los trabajos y de la línea de comandos del registro.
- Rechaza los repos públicos y se niega a ejecutarse como root. Detalle en [docs/security.md](docs/security.md).

## Límites

Solo repositorios: los runners de organización o grupo funcionan en GitHub pero no están
probados en las demás plataformas. En Forgejo, Gitea y GitLab los trabajos se ejecutan
directamente en tu equipo, no en un contenedor. Solo GitHub.com, no GitHub Enterprise Server.
Un trabajo a la vez por runner.

Al parar el servicio solo se espera a que termine un trabajo en marcha en Linux; en macOS y
Windows el sistema termina el proceso. `runnerkit stop`, `restart` y `remove` se niegan en
todos los sistemas mientras haya un trabajo, salvo con `--force`.

Probado de verdad: GitHub en Linux, macOS y Windows; Forgejo, Gitea y GitLab en Linux; Gitea y
GitLab también en macOS. Windows con Forgejo, Gitea o GitLab solo tiene pruebas unitarias. En
macOS, un runner que lanza `git` contra un servidor de tu red local puede quedar bloqueado
por la privacidad de red local del sistema (ver [docs/providers.md](docs/providers.md)). Cómo
funciona todo, y por qué, está en [docs/how-it-works.md](docs/how-it-works.md).

## Licencia

Apache-2.0. Ver [LICENSE](LICENSE) y [NOTICE](NOTICE).
