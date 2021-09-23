import React, { useState, useEffect, useMemo } from 'react';
import {
  InlineFieldRow,
  InlineField,
  Input,
  QueryField,
  SlatePrism,
  BracesPlugin,
  TypeaheadInput,
  TypeaheadOutput,
  Select,
} from '@grafana/ui';
import { tokenizer } from './syntax';
import Prism from 'prismjs';
import { Node } from 'slate';
import { css } from '@emotion/css';
import { isValidDuration, SelectableValue } from '@grafana/data';
import TempoLanguageProvider from './language_provider';
import { TempoDatasource, TempoQuery } from './datasource';
import { debounce } from 'lodash';

interface Props {
  datasource: TempoDatasource;
  query: TempoQuery;
  onChange: (value: TempoQuery) => void;
  onBlur?: () => void;
  onRunQuery: () => void;
}

const PRISM_LANGUAGE = 'tempo';
const durationPlaceholder = 'e.g. 1.2s, 100ms, 500us';
const plugins = [
  BracesPlugin(),
  SlatePrism({
    onlyIn: (node: Node) => node.object === 'block' && node.type === 'code_block',
    getSyntax: () => PRISM_LANGUAGE,
  }),
];

Prism.languages[PRISM_LANGUAGE] = tokenizer;

const NativeSearch = ({ datasource, query, onChange, onBlur, onRunQuery }: Props) => {
  const languageProvider = useMemo(() => new TempoLanguageProvider(datasource), [datasource]);
  const [hasSyntaxLoaded, setHasSyntaxLoaded] = useState(false);
  const [autocomplete, setAutocomplete] = useState<{
    serviceNameOptions: Array<SelectableValue<string>>;
    selectedServiceName: SelectableValue<string> | undefined;
    spanNameOptions: Array<SelectableValue<string>>;
    selectedSpanName: SelectableValue<string> | undefined;
  }>({
    serviceNameOptions: [],
    selectedServiceName: undefined,
    spanNameOptions: [],
    selectedSpanName: undefined,
  });
  const [validation, setValidation] = useState<{ [key: string]: boolean }>({});

  const fetchServiceNameOptions = useMemo(
    () =>
      debounce(
        async () => {
          const res = await languageProvider.getOptions('service.name');
          setAutocomplete((prev) => ({ ...prev, serviceNameOptions: res }));
        },
        500,
        { leading: true, trailing: true }
      ),
    [languageProvider]
  );

  const fetchSpanNameOptions = useMemo(
    () =>
      debounce(
        async () => {
          const res = await languageProvider.getOptions('name');
          setAutocomplete((prev) => ({ ...prev, spanNameOptions: res }));
        },
        500,
        { leading: true, trailing: true }
      ),
    [languageProvider]
  );

  useEffect(() => {
    const fetchAutocomplete = async () => {
      await languageProvider.start();
      await fetchServiceNameOptions();
      await fetchSpanNameOptions();
      setHasSyntaxLoaded(true);
    };
    fetchAutocomplete();
  }, [languageProvider, fetchServiceNameOptions, fetchSpanNameOptions]);

  const onTypeahead = async (typeahead: TypeaheadInput): Promise<TypeaheadOutput> => {
    return await languageProvider.provideCompletionItems(typeahead);
  };

  const cleanText = (text: string) => {
    const splittedText = text.split(/\s+(?=([^"]*"[^"]*")*[^"]*$)/g);
    if (splittedText.length > 1) {
      return splittedText[splittedText.length - 1];
    }
    return text;
  };

  const onKeyDown = (keyEvent: React.KeyboardEvent) => {
    if (keyEvent.key === 'Enter' && (keyEvent.shiftKey || keyEvent.ctrlKey)) {
      onRunQuery();
    }
  };

  return (
    <div className={css({ maxWidth: '500px' })}>
      <InlineFieldRow>
        <InlineField label="Service Name" labelWidth={14} grow>
          <Select
            menuShouldPortal
            options={autocomplete.serviceNameOptions}
            value={query.serviceName || ''}
            onChange={(v) => {
              onChange({
                ...query,
                serviceName: v?.value || undefined,
              });
            }}
            placeholder="Select a service"
            onOpenMenu={fetchServiceNameOptions}
            isClearable
            onKeyDown={onKeyDown}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField label="Span Name" labelWidth={14} grow>
          <Select
            menuShouldPortal
            options={autocomplete.spanNameOptions}
            value={query.spanName || ''}
            onChange={(v) => {
              onChange({
                ...query,
                spanName: v?.value || undefined,
              });
            }}
            placeholder="Select a span"
            onOpenMenu={fetchSpanNameOptions}
            isClearable
            onKeyDown={onKeyDown}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField label="Tags" labelWidth={14} grow tooltip="Values should be in the logfmt format.">
          <QueryField
            additionalPlugins={plugins}
            query={query.search}
            onTypeahead={onTypeahead}
            onBlur={onBlur}
            onChange={(value) => {
              onChange({
                ...query,
                search: value,
              });
            }}
            placeholder="http.status_code=200 error=true"
            cleanText={cleanText}
            onRunQuery={onRunQuery}
            syntaxLoaded={hasSyntaxLoaded}
            portalOrigin="tempo"
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField label="Min Duration" invalid={!!validation.minDuration} labelWidth={14} grow>
          <Input
            value={query.minDuration || ''}
            placeholder={durationPlaceholder}
            onBlur={() => {
              if (query.minDuration && !isValidDuration(query.minDuration)) {
                setValidation({ ...validation, minDuration: true });
              } else {
                setValidation({ ...validation, minDuration: false });
              }
            }}
            onChange={(v) =>
              onChange({
                ...query,
                minDuration: v.currentTarget.value,
              })
            }
            onKeyDown={onKeyDown}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField label="Max Duration" invalid={!!validation.maxDuration} labelWidth={14} grow>
          <Input
            value={query.maxDuration || ''}
            placeholder={durationPlaceholder}
            onBlur={() => {
              if (query.maxDuration && !isValidDuration(query.maxDuration)) {
                setValidation({ ...validation, maxDuration: true });
              } else {
                setValidation({ ...validation, maxDuration: false });
              }
            }}
            onChange={(v) =>
              onChange({
                ...query,
                maxDuration: v.currentTarget.value,
              })
            }
            onKeyDown={onKeyDown}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField
          label="Limit"
          invalid={!!validation.limit}
          labelWidth={14}
          grow
          tooltip="Maximum numbers of returned results. Defaults to 100."
        >
          <Input
            value={query.limit || ''}
            type="number"
            onChange={(v) => {
              let limit = v.currentTarget.value ? parseInt(v.currentTarget.value, 10) : undefined;
              if (limit && (!Number.isInteger(limit) || limit <= 0)) {
                setValidation({ ...validation, limit: true });
              } else {
                setValidation({ ...validation, limit: false });
              }

              onChange({
                ...query,
                limit,
              });
            }}
            onKeyDown={onKeyDown}
          />
        </InlineField>
      </InlineFieldRow>
    </div>
  );
};

export default NativeSearch;
