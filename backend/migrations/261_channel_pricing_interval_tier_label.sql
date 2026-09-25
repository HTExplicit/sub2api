-- 260 inserted a long-context pricing interval without tier_label. The column
-- is nullable, but channel pricing is read as text, so the NULL broke the
-- channel cache and the admin channel pages. Token intervals store ''.
UPDATE channel_pricing_intervals SET tier_label = '' WHERE tier_label IS NULL;
UPDATE channel_account_stats_pricing_intervals SET tier_label = '' WHERE tier_label IS NULL;
