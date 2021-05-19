# Sentinel Locations

[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/filecoin-project/sentinel-locations)
[![docker build status](https://img.shields.io/docker/cloud/build/filecoin/sentinel-locations?style=flat-square)](https://hub.docker.com/repository/docker/filecoin/sentinel-locations)
![Go](https://github.com/filecoin-project/sentinel-locations/workflows/Go/badge.svg)

A component of [**Sentinel**](https://github.com/filecoin-project/sentinel), a collection of services which monitor the health and function of the Filecoin network. 

**Sentinel-Locations** lookups and stores location information for miners based on the IPv4s declared on their on-chain multiaddresses.

## Usage

Define an environment variable `SENTINEL_DB` with the full sentinel db connection string and then run:

`./sentinel-locations`

The program will create the `locations` schema and `miners` table automatically.

## Code of Conduct

Sentinel Locations follows the [Filecoin Project Code of Conduct](https://github.com/filecoin-project/community/blob/master/CODE_OF_CONDUCT.md). Before contributing, please acquaint yourself with our social courtesies and expectations.


## Contributing

Welcoming [new issues](https://github.com/filecoin-project/sentinel-locations/issues/new) and [pull requests](https://github.com/filecoin-project/sentinel-locations/pulls).


## License

Sentinel Locations is dual-licensed under Apache 2.0 and MIT terms:

- Apache License, Version 2.0, ([LICENSE-APACHE](https://github.com/filecoin-project/sentinel-locations/blob/master/LICENSE-APACHE) or http://www.apache.org/licenses/LICENSE-2.0)
- MIT license ([LICENSE-MIT](https://github.com/filecoin-project/sentinel-locations/blob/master/LICENSE-MIT) or http://opensource.org/licenses/MIT)
