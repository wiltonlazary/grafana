---
aliases:
  - /docs/grafana/latest/datasources/
  - /docs/grafana/latest/datasources/overview/
title: Data sources
weight: 60
---

# Data sources

Grafana supports many different storage backends for your time series data (data source). Refer to [Add a data source]({{< relref "../administration/data-source-management/#add-a-data-source/" >}}) for instructions on how to add a data source to Grafana. Only users with the organization admin role can add data sources.

## Querying

Each data source has a specific Query Editor that is customized for the features and capabilities that the particular data source exposes. The query language and capabilities of each data source are obviously very different. You can combine data from multiple data sources onto a single Dashboard, but each Panel is tied to a specific data source that belongs to a particular Organization.

## Supported data sources

The following data sources are officially supported:

- [Alertmanager]({{< relref "./alertmanager/" >}})
- [AWS CloudWatch]({{< relref "./aws-cloudwatch/" >}})
- [Azure Monitor]({{< relref "./azuremonitor/" >}})
- [Elasticsearch]({{< relref "./elasticsearch/" >}})
- [Google Cloud Monitoring]({{< relref "./google-cloud-monitoring/" >}})
- [Graphite]({{< relref "./graphite/" >}})
- [InfluxDB]({{< relref "./influxdb/" >}})
- [Loki]({{< relref "./loki/" >}})
- [Microsoft SQL Server (MSSQL)]({{< relref "./mssql/" >}})
- [MySQL]({{< relref "./mysql/" >}})
- [OpenTSDB]({{< relref "./opentsdb/" >}})
- [PostgreSQL]({{< relref "./postgres/" >}})
- [Prometheus]({{< relref "./prometheus/" >}})
- [Jaeger]({{< relref "./jaeger/" >}})
- [Zipkin]({{< relref "./zipkin/" >}})
- [Tempo]({{< relref "./tempo/" >}})
- [Testdata]({{< relref "./testdata/" >}})

In addition to the data sources that you have configured in your Grafana, there are three special data sources available:

- **Grafana -** A built-in data source that generates random walk data. Useful for testing visualizations and running experiments.
- **Mixed -** Select this to query multiple data sources in the same panel. When this data source is selected, Grafana allows you to select a data source for every new query that you add.
  - The first query will use the data source that was selected before you selected **Mixed**.
  - You cannot change an existing query to use the Mixed Data Source.
  - Grafana Play example: [Mixed data sources](https://play.grafana.org/d/000000100/mixed-datasources?orgId=1)
- **Dashboard -** Select this to use a result set from another panel in the same dashboard.

## Data source plugins

Since Grafana 3.0 you can install data sources as plugins. Check out [Grafana.com/plugins](https://grafana.com/plugins) for more data sources.
