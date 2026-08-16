-- Provider identifiers become opaque text.
--
-- The platform never interprets a provider_id: it adopts whatever VPS Advisor
-- publishes in the catalog (contract A1) and echoes the same value back on the
-- results push (A4). `uuid` encoded an assumption about the website's own
-- primary keys that does not hold — VPS Advisor identifies a provider by its
-- slug, because its provider table is keyed on a BigAutoField that is not
-- stable across a database restore, while the slug is unique, indexed and the
-- provider's public identity.
--
-- With a uuid column the very first catalog sync failed on
--   invalid input syntax for type uuid: "examplehost"
-- and the builder aborts on `provider sync` before it reaches extraction or
-- artifact publication — so nothing is ever uploaded for workers to download.
--
-- Widening to text keeps every existing value valid (a uuid is text), keeps
-- the identifier opaque as the contract describes it, and costs nothing: these
-- columns are only ever compared for equality and joined on.
--
-- The foreign keys are dropped and recreated because PostgreSQL refuses to
-- alter the type of a column a constraint depends on.

alter table routing.asn              drop constraint asn_provider_id_fkey;
alter table routing.probe_target     drop constraint probe_target_provider_id_fkey;
alter table scheduling.assignment    drop constraint assignment_provider_id_fkey;

alter table routing.provider           alter column provider_id type text;
alter table routing.asn                alter column provider_id type text;
alter table routing.probe_target       alter column provider_id type text;
alter table scheduling.assignment      alter column provider_id type text;
alter table measurements.observation   alter column provider_id type text;
alter table aggregation.consensus_window alter column provider_id type text;
alter table aggregation.provider_status  alter column provider_id type text;
alter table aggregation.anomaly          alter column provider_id type text;

alter table routing.asn add constraint asn_provider_id_fkey
  foreign key (provider_id) references routing.provider (provider_id);
alter table routing.probe_target add constraint probe_target_provider_id_fkey
  foreign key (provider_id) references routing.provider (provider_id);
alter table scheduling.assignment add constraint assignment_provider_id_fkey
  foreign key (provider_id) references routing.provider (provider_id);
