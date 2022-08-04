import { render, screen, getAllByRole, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import { DataSourceInstanceSettings, DataSourcePluginMeta } from '@grafana/data';

import { LokiDatasource } from '../../datasource';
import { LokiOperationId, LokiVisualQuery } from '../types';

import { LokiQueryBuilder } from './LokiQueryBuilder';
import { EXPLAIN_LABEL_FILTER_CONTENT } from './LokiQueryBuilderExplained';

const defaultQuery: LokiVisualQuery = {
  labels: [{ op: '=', label: 'baz', value: 'bar' }],
  operations: [],
};

const createDefaultProps = () => {
  const datasource = new LokiDatasource(
    {
      url: '',
      jsonData: {},
      meta: {} as DataSourcePluginMeta,
    } as DataSourceInstanceSettings,
    undefined,
    undefined
  );

  const props = {
    datasource,
    onRunQuery: () => {},
    onChange: () => {},
    showExplain: false,
  };

  return props;
};

describe('LokiQueryBuilder', () => {
  it('tries to load labels when no labels are selected', async () => {
    const props = createDefaultProps();
    props.datasource.getDataSamples = jest.fn().mockResolvedValue([]);
    props.datasource.languageProvider.fetchSeriesLabels = jest.fn().mockReturnValue({ job: ['a'], instance: ['b'] });

    render(<LokiQueryBuilder {...props} query={defaultQuery} />);
    await userEvent.click(screen.getByLabelText('Add'));
    const labels = screen.getByText(/Labels/);
    const selects = getAllByRole(labels.parentElement!.parentElement!.parentElement!, 'combobox');
    await userEvent.click(selects[3]);
    await waitFor(() => expect(screen.getByText('job')).toBeInTheDocument());
  });

  it('shows error for query with operations and no stream selector', async () => {
    const query = { labels: [], operations: [{ id: LokiOperationId.Logfmt, params: [] }] };
    render(<LokiQueryBuilder {...createDefaultProps()} query={query} />);

    expect(
      await screen.findByText('You need to specify at least 1 label filter (stream selector)')
    ).toBeInTheDocument();
  });

  it('shows no error for query with empty __line_contains operation and no stream selector', async () => {
    const query = { labels: [], operations: [{ id: LokiOperationId.LineContains, params: [''] }] };
    render(<LokiQueryBuilder {...createDefaultProps()} query={query} />);

    await waitFor(() => {
      expect(
        screen.queryByText('You need to specify at least 1 label filter (stream selector)')
      ).not.toBeInTheDocument();
    });
  });
  it('shows explain section when showExplain is true', async () => {
    const query = {
      labels: [{ label: 'foo', op: '=', value: 'bar' }],
      operations: [{ id: LokiOperationId.LineContains, params: ['error'] }],
    };
    const props = createDefaultProps();
    props.showExplain = true;
    props.datasource.getDataSamples = jest.fn().mockResolvedValue([]);

    render(<LokiQueryBuilder {...props} query={query} />);
    expect(await screen.findByText(EXPLAIN_LABEL_FILTER_CONTENT)).toBeInTheDocument();
  });

  it('does not shows explain section when showExplain is false', async () => {
    const query = {
      labels: [{ label: 'foo', op: '=', value: 'bar' }],
      operations: [{ id: LokiOperationId.LineContains, params: ['error'] }],
    };
    const props = createDefaultProps();
    props.datasource.getDataSamples = jest.fn().mockResolvedValue([]);

    render(<LokiQueryBuilder {...props} query={query} />);
    await waitFor(() => {
      expect(screen.queryByText(EXPLAIN_LABEL_FILTER_CONTENT)).not.toBeInTheDocument();
    });
  });
});
