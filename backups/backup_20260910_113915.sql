--
-- PostgreSQL database dump
--

\restrict KLVMCDCO8EsRXPGbMgDqJM84T6IwTL0tvDgKXS1vRbie2s0eLhE6yGJlJHxDfdc

-- Dumped from database version 17.11 (Debian 17.11-1.pgdg12+2)
-- Dumped by pg_dump version 17.11 (Debian 17.11-1.pgdg12+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: learning_candidates; Type: TABLE; Schema: public; Owner: qqbot
--

CREATE TABLE public.learning_candidates (
    id character varying(128) NOT NULL,
    group_id bigint NOT NULL,
    kind character varying(64) NOT NULL,
    value text NOT NULL,
    meaning text NOT NULL,
    evidence_count integer NOT NULL,
    example_event_ids_json jsonb NOT NULL,
    confidence double precision NOT NULL,
    status character varying(32) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    target_user_id bigint DEFAULT 0 NOT NULL,
    promoted_memory_id character varying(128) DEFAULT ''::character varying NOT NULL,
    promoted_at timestamp with time zone
);


ALTER TABLE public.learning_candidates OWNER TO qqbot;

--
-- Name: learning_watermarks; Type: TABLE; Schema: public; Owner: qqbot
--

CREATE TABLE public.learning_watermarks (
    group_id bigint NOT NULL,
    kind character varying(64) NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    event_id character varying(128) NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


ALTER TABLE public.learning_watermarks OWNER TO qqbot;

--
-- Name: memories; Type: TABLE; Schema: public; Owner: qqbot
--

CREATE TABLE public.memories (
    memory_id character varying(128) NOT NULL,
    scope character varying(128) NOT NULL,
    type character varying(64) NOT NULL,
    subject character varying(255) NOT NULL,
    content text NOT NULL,
    source_event_id character varying(128) NOT NULL,
    descriptor_ref character varying(255) NOT NULL,
    confidence double precision NOT NULL,
    importance double precision NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone,
    updated_at timestamp with time zone NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    source_event_ids_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    source_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    origin character varying(32) DEFAULT 'agent'::character varying NOT NULL,
    supersedes_memory_id character varying(128) DEFAULT ''::character varying NOT NULL,
    recall_count integer DEFAULT 0 NOT NULL,
    last_recalled_at timestamp with time zone
);


ALTER TABLE public.memories OWNER TO qqbot;

--
-- Name: memory_claims; Type: TABLE; Schema: public; Owner: qqbot
--

CREATE TABLE public.memory_claims (
    claim_id character varying(128) NOT NULL,
    scope character varying(128) NOT NULL,
    type character varying(64) NOT NULL,
    subject character varying(255) NOT NULL,
    content text NOT NULL,
    evidence_event_ids_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    confidence double precision DEFAULT 0 NOT NULL,
    suggested_ttl character varying(64) DEFAULT ''::character varying NOT NULL,
    source character varying(32) DEFAULT 'model'::character varying NOT NULL,
    status character varying(32) DEFAULT 'staged'::character varying NOT NULL,
    supersedes_id character varying(128) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


ALTER TABLE public.memory_claims OWNER TO qqbot;

--
-- Data for Name: learning_candidates; Type: TABLE DATA; Schema: public; Owner: qqbot
--

COPY public.learning_candidates (id, group_id, kind, value, meaning, evidence_count, example_event_ids_json, confidence, status, created_at, target_user_id, promoted_memory_id, promoted_at) FROM stdin;
\.


--
-- Data for Name: learning_watermarks; Type: TABLE DATA; Schema: public; Owner: qqbot
--

COPY public.learning_watermarks (group_id, kind, occurred_at, event_id, updated_at) FROM stdin;
\.


--
-- Data for Name: memories; Type: TABLE DATA; Schema: public; Owner: qqbot
--

COPY public.memories (memory_id, scope, type, subject, content, source_event_id, descriptor_ref, confidence, importance, created_at, expires_at, updated_at, revision, source_event_ids_json, source_session_id, origin, supersedes_memory_id, recall_count, last_recalled_at) FROM stdin;
\.


--
-- Data for Name: memory_claims; Type: TABLE DATA; Schema: public; Owner: qqbot
--

COPY public.memory_claims (claim_id, scope, type, subject, content, evidence_event_ids_json, confidence, suggested_ttl, source, status, supersedes_id, created_at, updated_at) FROM stdin;
\.


--
-- Name: learning_candidates learning_candidates_pkey; Type: CONSTRAINT; Schema: public; Owner: qqbot
--

ALTER TABLE ONLY public.learning_candidates
    ADD CONSTRAINT learning_candidates_pkey PRIMARY KEY (id);


--
-- Name: learning_watermarks learning_watermarks_pkey; Type: CONSTRAINT; Schema: public; Owner: qqbot
--

ALTER TABLE ONLY public.learning_watermarks
    ADD CONSTRAINT learning_watermarks_pkey PRIMARY KEY (group_id, kind);


--
-- Name: memories memories_pkey; Type: CONSTRAINT; Schema: public; Owner: qqbot
--

ALTER TABLE ONLY public.memories
    ADD CONSTRAINT memories_pkey PRIMARY KEY (memory_id);


--
-- Name: memory_claims memory_claims_pkey; Type: CONSTRAINT; Schema: public; Owner: qqbot
--

ALTER TABLE ONLY public.memory_claims
    ADD CONSTRAINT memory_claims_pkey PRIMARY KEY (claim_id);


--
-- Name: idx_learning_candidates_group_status; Type: INDEX; Schema: public; Owner: qqbot
--

CREATE INDEX idx_learning_candidates_group_status ON public.learning_candidates USING btree (group_id, status, created_at);


--
-- Name: idx_memories_created; Type: INDEX; Schema: public; Owner: qqbot
--

CREATE INDEX idx_memories_created ON public.memories USING btree (created_at);


--
-- Name: idx_memories_scope_type; Type: INDEX; Schema: public; Owner: qqbot
--

CREATE INDEX idx_memories_scope_type ON public.memories USING btree (scope, type);


--
-- Name: idx_memory_claims_scope_status; Type: INDEX; Schema: public; Owner: qqbot
--

CREATE INDEX idx_memory_claims_scope_status ON public.memory_claims USING btree (scope, status, updated_at DESC);


--
-- PostgreSQL database dump complete
--

\unrestrict KLVMCDCO8EsRXPGbMgDqJM84T6IwTL0tvDgKXS1vRbie2s0eLhE6yGJlJHxDfdc

