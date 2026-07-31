export interface paths {
    "/api/auth/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * @description Browser login requires a same-origin `Origin` header, or a same-origin
         *     `Referer` when `Origin` is absent. The source host must match the HTTP
         *     `Host`; requests received over HTTPS must also use an HTTPS source URL.
         */
        post: operations["authLogin"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["authLogout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/auth/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["authMe"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/dashboard/get": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["dashboardGet"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/list-metric-samples": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverListMetricSamples"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/get-metric-history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverGetMetricHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/list-commands": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverListCommands"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/get-test": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverGetTest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/run-test": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverRunTest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server-test/catalog-status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverTestCatalogStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server-test/refresh-catalog": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverTestRefreshCatalog"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverCreate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/confirm-renewal": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverConfirmRenewal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverDelete"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/rotate-token": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverRotateToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/repair": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverRepair"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/upgrade-xray": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverUpgradeXray"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/upgrade-agent": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["serverUpgradeAgent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/server/list-release-versions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["serverListReleaseVersions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/provider/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["providerList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/provider/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["providerCreate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/provider/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["providerUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/provider/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["providerDelete"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/exchange-rate/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["exchangeRateList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/exchange-rate/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["exchangeRateRefresh"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/exchange-rate/save-custom": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["exchangeRateSaveCustom"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/exchange-rate/delete-custom": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["exchangeRateDeleteCustom"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/node/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["nodeList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/node/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["nodeCreate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/node/retry": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["nodeRetry"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/node/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["nodeDelete"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["chainList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["chainCreate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/edit": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** @description Creates a desired revision for ordered 1-4 hop topology and deploys changed pieces exit-to-entry. */
        post: operations["chainEdit"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/force-publish": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** @description Immediately promotes the desired revision as unconfirmed; queued Agent tasks continue without rollback. */
        post: operations["chainForcePublish"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/set-traffic-multiplier": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["chainSetTrafficMultiplier"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/reset-traffic": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["chainResetTraffic"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/get-traffic-history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["chainGetTrafficHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/retry": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["chainRetry"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/chain/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["chainDelete"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["userList"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/create": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** @description 创建真实用户；chain_ids 为直接链路分配，每项生成独立 access_uuid。 */
        post: operations["userCreate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["userUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/set-nodes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * @description 原子语义上的期望分配。node_ids 保留独立节点兼容；chain_ids 是用户与链的直接多对多分配。
         *     未变化的链分配保留 access_uuid，新增分配生成新凭证并重算共享入口。
         */
        post: operations["userSetNodes"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["userDelete"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/sub-settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["userSubSettings"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/user/traffic-history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["userTrafficHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/setting/get": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["settingGet"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/setting/sub": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["settingSubGet"];
        put?: never;
        post: operations["settingSubUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/setting/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["settingUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/setting/change-password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["settingChangePassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/setting/test-alerts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["settingTestAlerts"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/panel/restart": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["panelRestart"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/panel/state": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["panelState"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/panel/get-version": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["panelGetVersion"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/panel/start-update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["panelStartUpdate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/panel/get-update-status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["panelGetUpdateStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/backup/download": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["backupDownload"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/log/list-operations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["logListOperations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/log/clear-operations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["logClearOperations"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/log/list-requests": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["logListRequests"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/log/clear-requests": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post: operations["logClearRequests"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        MessageID: string;
        CertInfo: {
            common_name: string;
            /** @default [] */
            dns_names: string[];
            /** Format: date-time */
            not_after: string;
            expired: boolean;
        };
        VirtualConfig: {
            protocol: string;
            port?: number;
            flow?: string;
            network?: string;
            service_name?: string;
            path?: string;
            mode?: string;
            host?: string;
            method?: string;
            fingerprint?: string;
            encryption?: string;
            template: {
                [key: string]: unknown;
            };
        };
        /** @enum {string} */
        CommandStatus: "queued" | "sent" | "acked" | "failed" | "abandoned";
        AgentSettings: {
            /** @description Read-only to clients; panel increments it when saving. */
            revision: number;
            reconnect: {
                /**
                 * @default infinite
                 * @enum {string}
                 */
                mode: "infinite" | "limited";
                /** @default 10 */
                max_retries: number;
            };
            telemetry: {
                /** @default 60 */
                interval_seconds: number;
            };
            drift_detection: {
                /** @default 15 */
                interval_seconds: number;
            };
        };
        /** @description Existing panel fields plus the complete global Agent settings object. */
        SettingUpdateRequest: {
            /**
             * @description IANA timezone used for chain traffic day/month bucket boundaries.
             * @default Asia/Shanghai
             */
            traffic_timezone: string;
            agent?: components["schemas"]["AgentSettings"];
            release_inspection?: components["schemas"]["ReleaseInspectionSettings"];
            billing_inspection?: components["schemas"]["InspectionSchedule"];
            exchange_rate_inspection?: components["schemas"]["InspectionSchedule"];
            reporting_currency?: components["schemas"]["Currency"];
        } & {
            [key: string]: unknown;
        };
        /**
         * @description Supported ISO 4217 currency code.
         * @enum {string}
         */
        Currency: "AUD" | "BRL" | "CAD" | "CHF" | "CNY" | "CZK" | "DKK" | "EUR" | "GBP" | "HKD" | "HUF" | "IDR" | "ILS" | "INR" | "ISK" | "JPY" | "KRW" | "MXN" | "MYR" | "NOK" | "NZD" | "PHP" | "PLN" | "RON" | "SEK" | "SGD" | "THB" | "TRY" | "USD" | "ZAR";
        BillingInput: {
            /** @default false */
            enabled: boolean;
            provider_id?: number;
            /** @description Original price in the currency's minor unit. */
            amount_minor?: number;
            currency?: components["schemas"]["Currency"];
            /** Format: date */
            service_started_on?: string;
            interval_count?: number;
            /** @enum {string} */
            interval_unit?: "day" | "month" | "year";
            /** Format: date */
            next_renewal_on?: string;
        };
        ConvertedCost: {
            /** @description Converted value in the reporting currency's minor unit. */
            amount_minor: number;
            currency: components["schemas"]["Currency"];
            /** @description Public cache date; empty for an identity conversion. */
            rate_date: string;
            /** @enum {string} */
            source: "identity" | "frankfurter" | "custom_anchor";
            /** @description Present for custom_anchor results. */
            anchor_currency?: string;
        };
        /** @description Server billing data. Public conversion is always attempted; custom conversion is present only when an enabled anchor target matches the reporting currency. */
        BillingProfile: {
            enabled?: boolean;
            amount_minor?: number;
            currency?: components["schemas"]["Currency"];
            public_converted?: components["schemas"]["ConvertedCost"];
            custom_converted?: components["schemas"]["ConvertedCost"];
        };
        TrafficPlanInput: {
            /** @description Decimal bytes; null means unlimited. */
            quota_bytes: number | null;
            /** @enum {string} */
            accounting_mode: "outbound" | "bidirectional" | "max";
            /** Format: date */
            reset_anchor_on: string;
            reset_count: number;
            /** @enum {string} */
            reset_unit: "day" | "month" | "year";
        };
        ConfirmRenewalRequest: {
            server_id: number;
            /**
             * Format: date
             * @description Must be later than today in the panel timezone.
             */
            next_renewal_on: string;
        };
        /** @description Source currency is unique. On create, target_currency is overwritten with the panel reporting currency. At least one amount must equal 1. A rate is applied only while enabled and its target matches the current reporting currency. */
        CustomExchangeRate: {
            /** @description Zero creates a new rate; a positive ID updates it. */
            id: number;
            source_currency: components["schemas"]["Currency"];
            /** @description Source-side major-unit amount; this or target_amount must equal 1. */
            source_amount: string;
            target_currency: components["schemas"]["Currency"];
            /** @description Reporting-currency major-unit amount; this or source_amount must equal 1. */
            target_amount: string;
            enabled: boolean;
        };
        InspectionSchedule: {
            every: number;
            /** @enum {string} */
            unit: "minute" | "hour" | "day" | "month" | "year";
            at?: string;
        };
        ReleaseInspectionSettings: {
            agent: components["schemas"]["InspectionSchedule"];
            xray: components["schemas"]["InspectionSchedule"];
        };
        /** @enum {string} */
        RPCCode: "OK" | "ACCEPTED" | "AUTH_REQUIRED" | "AUTH_INVALID_CREDENTIALS" | "INVALID_ARGUMENT" | "NOT_FOUND" | "CONFLICT" | "OPERATION_LOCKED" | "UNSUPPORTED_ACTION" | "INTERNAL_ERROR" | "UPSTREAM_ERROR" | "SERVICE_UNAVAILABLE" | "SERVER_OFFLINE" | "PORT_OUT_OF_RANGE" | "UPDATE_IN_PROGRESS";
        ProtocolCode: string;
        ProtocolError: {
            /** @enum {string} */
            code: "HTTP_400" | "HTTP_404" | "HTTP_405" | "HTTP_413" | "HTTP_415" | "HTTP_429";
            message: string;
            data: null;
            request_id: components["schemas"]["MessageID"];
            trace_id: components["schemas"]["MessageID"];
        };
        RPCEnvelope: {
            code: components["schemas"]["RPCCode"] | components["schemas"]["ProtocolCode"];
            message: string;
            data: unknown;
            request_id: components["schemas"]["MessageID"];
            trace_id: components["schemas"]["MessageID"];
        };
    };
    responses: {
        /** @description Completed Lattix RPC; inspect body code for business outcome. */
        RPCResponse: {
            headers: {
                "X-Request-ID"?: components["schemas"]["MessageID"];
                "X-Trace-ID"?: components["schemas"]["MessageID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RPCEnvelope"];
            };
        };
        /** @description Protocol-layer HTTP 400, 404, 405, 413, 415, or 429 response. */
        ProtocolErrorResponse: {
            headers: {
                "X-Request-ID"?: components["schemas"]["MessageID"];
                "X-Trace-ID"?: components["schemas"]["MessageID"];
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["ProtocolError"];
            };
        };
        /** @description Request rejected by a bounded authentication workload or login rate limiter. */
        RateLimitErrorResponse: {
            headers: {
                /** @description Whole seconds before the client should retry. */
                "Retry-After"?: number;
                "X-Request-ID"?: components["schemas"]["MessageID"];
                "X-Trace-ID"?: components["schemas"]["MessageID"];
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["ProtocolError"];
            };
        };
    };
    parameters: {
        ServerID: number;
        /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
        CSRFToken: string;
        /** @description Client-generated key scoped to the authenticated operator and RPC route. */
        IdempotencyKey: string;
    };
    requestBodies: {
        RPCBody: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
    };
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    authLogin: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            429: components["responses"]["RateLimitErrorResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    authLogout: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    authMe: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    dashboardGet: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverListMetricSamples: {
        parameters: {
            query?: {
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverGetMetricHistory: {
        parameters: {
            query: {
                server_id: components["parameters"]["ServerID"];
                hours?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverListCommands: {
        parameters: {
            query: {
                server_id: components["parameters"]["ServerID"];
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverGetTest: {
        parameters: {
            query: {
                server_id: components["parameters"]["ServerID"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverRunTest: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverTestCatalogStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverTestRefreshCatalog: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverCreate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverConfirmRenewal: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ConfirmRenewalRequest"];
            };
        };
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverDelete: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverRotateToken: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverRepair: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverUpgradeXray: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverUpgradeAgent: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    serverListReleaseVersions: {
        parameters: {
            query: {
                kind: "agent" | "xray";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    providerList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    providerCreate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    providerUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    providerDelete: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    exchangeRateList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    exchangeRateRefresh: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    exchangeRateSaveCustom: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CustomExchangeRate"];
            };
        };
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    exchangeRateDeleteCustom: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    nodeList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    nodeCreate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    nodeRetry: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    nodeDelete: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainCreate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainEdit: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainForcePublish: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainSetTrafficMultiplier: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainResetTraffic: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainGetTrafficHistory: {
        parameters: {
            query: {
                chain_id: number;
                hop_id?: number;
                days?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainRetry: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    chainDelete: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userList: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userCreate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userSetNodes: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userDelete: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userSubSettings: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    userTrafficHistory: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingGet: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingSubGet: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingSubUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SettingUpdateRequest"];
            };
        };
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingChangePassword: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            429: components["responses"]["RateLimitErrorResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    settingTestAlerts: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    panelRestart: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    panelState: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    panelGetVersion: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    panelStartUpdate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    panelGetUpdateStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    backupDownload: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description SQLite attachment or RPC error envelope */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/octet-stream": string;
                    "application/json": components["schemas"]["RPCEnvelope"];
                };
            };
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    logListOperations: {
        parameters: {
            query?: {
                severity?: string;
                category?: string;
                server_id?: number;
                operator?: string;
                q?: string;
                from?: string;
                to?: string;
                limit?: number;
                offset?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    logClearOperations: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    logListRequests: {
        parameters: {
            query?: {
                limit?: 10 | 30 | 50 | 100;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
    logClearRequests: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound token returned by `/api/auth/login` and `/api/auth/me`. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
                /** @description Client-generated key scoped to the authenticated operator and RPC route. */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: components["requestBodies"]["RPCBody"];
        responses: {
            200: components["responses"]["RPCResponse"];
            default: components["responses"]["ProtocolErrorResponse"];
        };
    };
}

export type RPCCode = components['schemas']['RPCCode']
export type ProtocolCode = `HTTP_${number}`

export const rpcCodes = ["OK","ACCEPTED","AUTH_REQUIRED","AUTH_INVALID_CREDENTIALS","INVALID_ARGUMENT","NOT_FOUND","CONFLICT","OPERATION_LOCKED","UNSUPPORTED_ACTION","INTERNAL_ERROR","UPSTREAM_ERROR","SERVICE_UNAVAILABLE","SERVER_OFFLINE","PORT_OUT_OF_RANGE","UPDATE_IN_PROGRESS"] as const
const rpcCodeSet = new Set<string>(rpcCodes)

export function isRPCCode(value: unknown): value is RPCCode {
  return typeof value === 'string' && rpcCodeSet.has(value)
}

export interface RPCEnvelope<T = unknown> {
  code: RPCCode | ProtocolCode
  message: string
  data: T
  request_id: string
  trace_id: string
}

export const rpcOperations = {
  authLogin: { method: 'POST', path: '/api/auth/login' },
  authLogout: { method: 'POST', path: '/api/auth/logout' },
  authMe: { method: 'GET', path: '/api/auth/me' },
  dashboardGet: { method: 'GET', path: '/api/dashboard/get' },
  serverList: { method: 'GET', path: '/api/server/list' },
  serverListMetricSamples: { method: 'GET', path: '/api/server/list-metric-samples' },
  serverGetMetricHistory: { method: 'GET', path: '/api/server/get-metric-history' },
  serverListCommands: { method: 'GET', path: '/api/server/list-commands' },
  serverGetTest: { method: 'GET', path: '/api/server/get-test' },
  serverRunTest: { method: 'POST', path: '/api/server/run-test' },
  serverTestCatalogStatus: { method: 'GET', path: '/api/server-test/catalog-status' },
  serverTestRefreshCatalog: { method: 'POST', path: '/api/server-test/refresh-catalog' },
  serverCreate: { method: 'POST', path: '/api/server/create' },
  serverUpdate: { method: 'POST', path: '/api/server/update' },
  serverConfirmRenewal: { method: 'POST', path: '/api/server/confirm-renewal' },
  serverDelete: { method: 'POST', path: '/api/server/delete' },
  serverRotateToken: { method: 'POST', path: '/api/server/rotate-token' },
  serverRepair: { method: 'POST', path: '/api/server/repair' },
  serverUpgradeXray: { method: 'POST', path: '/api/server/upgrade-xray' },
  serverUpgradeAgent: { method: 'POST', path: '/api/server/upgrade-agent' },
  serverListReleaseVersions: { method: 'GET', path: '/api/server/list-release-versions' },
  providerList: { method: 'GET', path: '/api/provider/list' },
  providerCreate: { method: 'POST', path: '/api/provider/create' },
  providerUpdate: { method: 'POST', path: '/api/provider/update' },
  providerDelete: { method: 'POST', path: '/api/provider/delete' },
  exchangeRateList: { method: 'GET', path: '/api/exchange-rate/list' },
  exchangeRateRefresh: { method: 'POST', path: '/api/exchange-rate/refresh' },
  exchangeRateSaveCustom: { method: 'POST', path: '/api/exchange-rate/save-custom' },
  exchangeRateDeleteCustom: { method: 'POST', path: '/api/exchange-rate/delete-custom' },
  nodeList: { method: 'GET', path: '/api/node/list' },
  nodeCreate: { method: 'POST', path: '/api/node/create' },
  nodeRetry: { method: 'POST', path: '/api/node/retry' },
  nodeDelete: { method: 'POST', path: '/api/node/delete' },
  chainList: { method: 'GET', path: '/api/chain/list' },
  chainCreate: { method: 'POST', path: '/api/chain/create' },
  chainEdit: { method: 'POST', path: '/api/chain/edit' },
  chainForcePublish: { method: 'POST', path: '/api/chain/force-publish' },
  chainSetTrafficMultiplier: { method: 'POST', path: '/api/chain/set-traffic-multiplier' },
  chainResetTraffic: { method: 'POST', path: '/api/chain/reset-traffic' },
  chainGetTrafficHistory: { method: 'GET', path: '/api/chain/get-traffic-history' },
  chainRetry: { method: 'POST', path: '/api/chain/retry' },
  chainDelete: { method: 'POST', path: '/api/chain/delete' },
  userList: { method: 'GET', path: '/api/user/list' },
  userCreate: { method: 'POST', path: '/api/user/create' },
  userUpdate: { method: 'POST', path: '/api/user/update' },
  userSetNodes: { method: 'POST', path: '/api/user/set-nodes' },
  userDelete: { method: 'POST', path: '/api/user/delete' },
  userSubSettings: { method: 'POST', path: '/api/user/sub-settings' },
  userTrafficHistory: { method: 'GET', path: '/api/user/traffic-history' },
  settingGet: { method: 'GET', path: '/api/setting/get' },
  settingSubGet: { method: 'GET', path: '/api/setting/sub' },
  settingSubUpdate: { method: 'POST', path: '/api/setting/sub' },
  settingUpdate: { method: 'POST', path: '/api/setting/update' },
  settingChangePassword: { method: 'POST', path: '/api/setting/change-password' },
  settingTestAlerts: { method: 'POST', path: '/api/setting/test-alerts' },
  panelRestart: { method: 'POST', path: '/api/panel/restart' },
  panelState: { method: 'GET', path: '/api/panel/state' },
  panelGetVersion: { method: 'GET', path: '/api/panel/get-version' },
  panelStartUpdate: { method: 'POST', path: '/api/panel/start-update' },
  panelGetUpdateStatus: { method: 'GET', path: '/api/panel/get-update-status' },
  backupDownload: { method: 'GET', path: '/api/backup/download' },
  logListOperations: { method: 'GET', path: '/api/log/list-operations' },
  logClearOperations: { method: 'POST', path: '/api/log/clear-operations' },
  logListRequests: { method: 'GET', path: '/api/log/list-requests' },
  logClearRequests: { method: 'POST', path: '/api/log/clear-requests' },
} as const

type RPCOperation = (typeof rpcOperations)[keyof typeof rpcOperations]
export type RPCMethod = RPCOperation['method']
export type RPCPathByMethod<M extends RPCMethod> = Extract<RPCOperation, { method: M }>['path']
