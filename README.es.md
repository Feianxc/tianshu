<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Logotipo de Tianshu: la Osa Mayor con su primera estrella, Dubhe, encendida">

# Tianshu 天枢

**Un panel de control autoalojado para los servidores, dispositivos, servicios, proyectos y repositorios de una persona, con un servidor MCP de solo lectura para que los agentes de IA vean lo mismo que tú.**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Vista general de Tianshu: mapa estelar en vivo, telemetría de los últimos 60 minutos por host y lista de pendientes](docs/images/overview-dark.png)

Tianshu (天枢, *Dubhe*, la primera estrella de la Osa Mayor) es un único binario de Go con una consola web integrada. Responde de un vistazo a una pregunta: **¿hay algo que necesite mi atención ahora?** Mantiene un catálogo de lo que tienes, reúne pruebas reales de su estado, lo convierte en una lista corta de hallazgos y expone el mismo modelo de lectura a los agentes de IA mediante el [Model Context Protocol](https://modelcontextprotocol.io).

## Funciones

- Dispositivos, servicios, proyectos, repositorios y **referencias** a credenciales, separados por espacios de trabajo (personal, empresa, clientes).
- Mapa estelar en vivo: cada espacio es una órbita; forma y color indican la salud, y un pulso viaja al centro cada vez que un host informa.
- Franja de telemetría: los últimos 60 minutos de informes reales por host. Sin datos se ve un hueco, nunca «OK».
- Salud, frescura, ciclo de vida y aceptación se registran por separado; los datos caducados no cuentan como sanos.
- Hallazgos sin duplicados que se cierran solos cuando el problema desaparece; posponer un día, una semana o un mes.
- Agente para Linux y Windows, recolección SSH de solo lectura y sondas HTTP, TCP y TLS.
- Vigilancia de la exposición SSH: inicio con contraseña, intentos fallidos, fail2ban y huellas de claves.
- Servidor MCP (tokens Bearer y OAuth 2.1), aislamiento entre espacios, sin secretos almacenados y auditoría de cada llamada.
- Inicio de sesión por emparejamiento de dispositivos, paleta de comandos (`Ctrl K`), temas oscuro y claro, diseño móvil y envoltorio para Android.

## Inicio rápido

Requiere Go 1.27+ y Node.js 20+.

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # opcional: datos de demostración ficticios
.build/wanglou init                      # muestra el token de administrador una sola vez
.build/wanglou serve                     # http://127.0.0.1:8787
```

Más detalles en [Arquitectura](docs/ARCHITECTURE.md) y [Despliegue](docs/DEPLOYMENT.md) (en inglés). La interfaz de la consola está por ahora en chino simplificado.

## Licencia

[MIT](LICENSE) © 2026 Feianxc
