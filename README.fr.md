<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Logo de Tianshu : la Grande Ourse avec sa première étoile, Dubhe, allumée">

# Tianshu 天枢

**Un plan de contrôle auto-hébergé pour les serveurs, appareils, services, projets et dépôts d'une personne, avec un serveur MCP en lecture seule pour que les agents IA voient ce que vous voyez.**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Vue d'ensemble de Tianshu : carte stellaire en direct, télémétrie des 60 dernières minutes par hôte et liste des points à traiter](docs/images/overview-dark.png)

Tianshu (天枢, *Dubhe*, la première étoile de la Grande Ourse) est un binaire Go unique avec une console web intégrée. Il répond d'un coup d'œil à une question : **est-ce que quelque chose a besoin de moi maintenant ?** Il tient un catalogue de ce que vous possédez, collecte des preuves réelles de son état, en tire une courte liste de constats et expose le même modèle de lecture aux agents IA via le [Model Context Protocol](https://modelcontextprotocol.io).

## Fonctionnalités

- Appareils, services, projets, dépôts et **références** d'identifiants, séparés par espaces de travail (personnel, entreprise, clients).
- Carte stellaire en direct : une orbite par espace, la forme et la couleur indiquent la santé ; une impulsion rejoint le centre à chaque rapport d'un hôte.
- Bande de télémétrie : les 60 dernières minutes de rapports réels par hôte. Sans données, on voit un trou, jamais « OK ».
- Santé, fraîcheur, cycle de vie et recette suivis séparément ; des données périmées ne comptent jamais comme saines.
- Constats dédupliqués qui se ferment seuls une fois le problème résolu ; report d'un jour, d'une semaine ou d'un mois.
- Agent pour Linux et Windows, collecte SSH en lecture seule, sondes HTTP, TCP et TLS.
- Surveillance de l'exposition SSH : connexion par mot de passe, échecs de connexion, fail2ban, empreintes de clés.
- Serveur MCP (jetons Bearer et OAuth 2.1), isolation des espaces, aucun secret stocké, audit de chaque appel.
- Connexion par appairage d'appareils, palette de commandes (`Ctrl K`), thèmes sombre et clair, affichage mobile et coque Android.

## Démarrage rapide

Nécessite Go 1.27+ et Node.js 20+.

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # facultatif : données de démonstration fictives
.build/wanglou init                      # affiche le jeton administrateur une seule fois
.build/wanglou serve                     # http://127.0.0.1:8787
```

Voir [Architecture](docs/ARCHITECTURE.md) et [Déploiement](docs/DEPLOYMENT.md) (en anglais). L'interface de la console est pour l'instant en chinois simplifié.

## Licence

[MIT](LICENSE) © 2026 Feianxc
