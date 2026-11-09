<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Logo do Tianshu: a Ursa Maior com sua primeira estrela, Dubhe, acesa">

# Tianshu 天枢

**Um painel de controle auto-hospedado para os servidores, dispositivos, serviços, projetos e repositórios de uma pessoa, com um servidor MCP somente leitura para que agentes de IA vejam o mesmo que você.**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Visão geral do Tianshu: mapa estelar ao vivo, telemetria dos últimos 60 minutos por host e lista de pendências](docs/images/overview-dark.png)

Tianshu (天枢, *Dubhe*, a primeira estrela da Ursa Maior) é um único binário Go com um console web embutido. Ele responde de relance a uma pergunta: **existe algo que precisa de mim agora?** Mantém um catálogo do que você possui, coleta evidências reais do estado de cada item, transforma isso em uma lista curta de achados e expõe o mesmo modelo de leitura para agentes de IA via [Model Context Protocol](https://modelcontextprotocol.io).

## Recursos

- Dispositivos, serviços, projetos, repositórios e **referências** a credenciais, separados por espaços de trabalho (pessoal, empresa, clientes).
- Mapa estelar ao vivo: cada espaço é uma órbita, forma e cor indicam a saúde e um pulso viaja até o centro a cada relatório de um host.
- Faixa de telemetria: os últimos 60 minutos de relatórios reais por host. Sem dados, aparece um vazio, nunca "OK".
- Saúde, atualidade, ciclo de vida e aceitação registrados separadamente; dados vencidos nunca contam como saudáveis.
- Achados sem duplicatas que se fecham sozinhos quando o problema some; adiar por um dia, uma semana ou um mês.
- Agente para Linux e Windows, coleta SSH somente leitura e sondas HTTP, TCP e TLS.
- Monitoramento da exposição SSH: login por senha, falhas de login, fail2ban e impressões digitais de chaves.
- Servidor MCP (tokens Bearer e OAuth 2.1), isolamento entre espaços, nenhum segredo armazenado e auditoria de cada chamada.
- Login por pareamento de dispositivos, paleta de comandos (`Ctrl K`), temas escuro e claro, layout móvel e app Android.

## Início rápido

Requer Go 1.27+ e Node.js 20+.

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # opcional: dados de demonstração fictícios
.build/wanglou init                      # mostra o token de administrador uma única vez
.build/wanglou serve                     # http://127.0.0.1:8787
```

Veja [Arquitetura](docs/ARCHITECTURE.md) e [Implantação](docs/DEPLOYMENT.md) (em inglês). A interface do console está, por enquanto, em chinês simplificado.

## Licença

[MIT](LICENSE) © 2026 Feianxc
