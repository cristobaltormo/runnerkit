# runnerkit

[English](README.md) | Español

[![CI](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml/badge.svg)](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Ejecuta tus GitHub Actions en tu propio ordenador en vez de gastar los minutos de GitHub.

runnerkit instala el runner oficial de GitHub en Linux, macOS o Windows con un comando y lo
deja funcionando como servicio en segundo plano. Lo reinicia si falla, lo pausa si el
portátil va con batería, limpia las carpetas de compilación viejas y se niega a ejecutarse
como root. Tus workflows se quedan como están, salvo una línea.

```
runnerkit add mi-usuario/mi-repo-privado
```

```
Runner portatil-mi-usuario-mi-repo-privado is installed and running.
To use it, put this in your workflow:

  runs-on: [self-hosted, linux, x64, runnerkit]
```

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

Hace falta un token de GitHub con permiso de administración sobre el repositorio. Lo más
fácil es el CLI de GitHub: ejecuta `gh auth login` una vez y runnerkit recoge el token.
runnerkit lo busca en `--token`, luego `RUNNERKIT_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` y por
último `gh`. Solo se usa mientras corre el comando y runnerkit nunca lo escribe en disco.

```
runnerkit add usuario/repo                 un repositorio
runnerkit add mi-org --group Default       una organización, con grupo de runners opcional
runnerkit add usuario/repo --labels gpu    etiquetas extra
runnerkit list                             runners de este equipo y su estado
runnerkit status NOMBRE                    detalle de uno
runnerkit logs NOMBRE -n 100               final de su registro
runnerkit stop NOMBRE                      también start y restart; --force para con un trabajo en marcha
runnerkit clean --dry-run                  qué carpetas viejas se borrarían
runnerkit remove NOMBRE                    lo da de baja en GitHub y borra todo
```

Otras opciones de `add`: `--name`, `--no-start`, `--no-pause-on-battery`, `--allow-public` y
`--replace` (sustituye un runner del mismo nombre que ya esté registrado).
`clean --days N` cambia la antigüedad mínima de una carpeta de trabajo. `remove --force`
borra en local cuando no se puede llegar a GitHub.

En Linux, ejecuta una vez `loginctl enable-linger $USER` para que el runner siga activo al
cerrar sesión. En macOS y Windows el runner arranca al iniciar sesión y se detiene al cerrarla.

## Qué hace

- Instala el runner oficial y comprueba antes su SHA-256; no continúa sin él.
- Lo ejecuta como servicio de usuario: systemd en Linux, launchd en macOS y el Programador de
  tareas en Windows. No pide permisos de administrador.
- Lo reinicia si falla, con una espera creciente.
- Lo pausa con batería cuando no hay ningún trabajo en marcha, y nunca interrumpe un trabajo por la batería.
- Lo ejecuta con prioridad baja para que el equipo siga siendo usable.
- Borra carpetas de trabajo y registros viejos.
- Mantiene tu token de GitHub fuera de los trabajos y de la línea de comandos del registro.
- Rechaza los repos públicos y se niega a ejecutarse como root. Detalle en [docs/security.md](docs/security.md).

## Límites

Solo GitHub.com por ahora; los runners de Forgejo y Gitea están previstos. Un trabajo a la
vez por runner. Al parar el servicio solo se espera a que termine un trabajo en marcha en
Linux; en macOS y Windows el sistema termina el proceso. `runnerkit stop`, `restart` y
`remove` se niegan en todos los sistemas mientras haya un trabajo, salvo con `--force`. El
código de macOS y Windows está cubierto por pruebas de los
ficheros y comandos que genera y tiene menos uso real que el de Linux. Cómo funciona todo, y
por qué, está en [docs/how-it-works.md](docs/how-it-works.md).

## Licencia

Apache-2.0. Ver [LICENSE](LICENSE) y [NOTICE](NOTICE).
