--
-- PostgreSQL database dump
--

-- Dumped from database version 17.5 (Homebrew)
-- Dumped by pg_dump version 17.5 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Data for Name: tenants; Type: TABLE DATA; Schema: public; Owner: -
--

SET session_replication_role = replica;

-- 先清空 schema.sql 播种的默认数据，再全量写入迁移数据。
TRUNCATE TABLE
    public.role_permissions, public.role_menus,
    public.project_members, public.team_projects, public.team_members, public.team_admins,
    public.people, public.users, public.roles, public.teams,
    team.projects, solo.projects, public.system_settings, public.tenants
CASCADE;

INSERT INTO public.tenants (id, name, code, status, created_at) VALUES ('11111111-1111-4111-8111-111111111111', '默认公司', 'default', 'disabled', '2026-11-18 10:58:49.109703+08');
INSERT INTO public.tenants (id, name, code, status, created_at) VALUES ('86128ddb-bd67-4d9d-b5db-6c233a80920f', '世界第一家公司', 'wordone', 'active', '2026-11-18 17:11:12.837186+08');
INSERT INTO public.tenants (id, name, code, status, created_at) VALUES ('74e6a23f-c81b-465a-a015-157d8759235c', '世界第二家公司', 'worldcompany2', 'active', '2026-11-19 10:53:49.824307+08');


--
-- Data for Name: roles; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'tester', '测试', '可以查看与下载应用包', true, '2026-11-19 10:53:49.842327+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'guest', '访客', '只读，适合外部协作方', true, '2026-11-19 10:53:49.843482+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'project-owner', '项目负责人', '负责项目整体推进：可管理项目与应用包，但不参与平台配置', true, '2026-11-18 21:14:25.328126+08', '11111111-1111-4111-8111-111111111111');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'developer', '开发', '可以上传、发布与删除应用包', true, '2026-11-18 21:14:25.330916+08', '11111111-1111-4111-8111-111111111111');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'tester', '测试', '可以查看与下载应用包', true, '2026-11-18 21:14:25.332004+08', '11111111-1111-4111-8111-111111111111');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'guest', '访客', '只读，适合外部协作方', true, '2026-11-18 21:14:25.332678+08', '11111111-1111-4111-8111-111111111111');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'developer', '开发', '可以上传、发布与删除应用包', true, '2026-11-18 21:14:25.334591+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'tester', '测试', '可以查看与下载应用包', true, '2026-11-18 21:14:25.335517+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'guest', '访客', '只读，适合外部协作方', true, '2026-11-18 21:14:25.33619+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'project-owner', '项目负责人', '负责项目整体推进：可管理项目与应用包，但不参与平台配置', true, '2026-11-18 21:14:25.333545+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'project-owner', '项目负责人', '负责项目整体推进：可管理项目与应用包，但不参与平台配置', true, '2026-11-19 10:53:49.83765+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'developer', '开发', '可以上传、发布与删除应用包', true, '2026-11-19 10:53:49.841015+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.roles (id, key, name, description, is_builtin, created_at, tenant_id) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'teshu', '特殊角色', '', false, '2026-11-19 15:48:39.694826+08', '74e6a23f-c81b-465a-a015-157d8759235c');


--
-- Data for Name: people; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('cb7f9a03-af72-49ab-bf44-3bfa1778d5cb', '陈冬', 'chendong', '', '', '前端组长', '2026-11-18 19:49:45.572694+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f', '1a5d2308-400c-4896-87b8-821f9c035c3b');
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('b2393d84-6ba9-4034-847e-f6d8f2f2e50c', '军子', 'xuzhijun', '', '', '前端开发', '2026-11-19 11:30:29.128041+08', '74e6a23f-c81b-465a-a015-157d8759235c', '4312a819-b236-4bc5-8214-c8ac37e6ffd5');
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('e7459483-a42a-4441-aca4-198d4282af70', '邹亮', 'zouliang', '', '', '', '2026-11-18 19:50:32.639151+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f', NULL);
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('7bb2b732-9ea9-4e97-bf68-8745fade0e0b', '文婷', 'wenting', '', '', '', '2026-11-18 20:19:24.455653+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f', NULL);
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('eed39ad6-a992-43a8-b34c-29b0c6e9e6ba', 'linanan', 'linanan', '1658546595@qq.com', '13163723621', '前端开发', '2026-11-16 15:37:25.452711+08', '11111111-1111-4111-8111-111111111111', 'b48e3adb-2208-4377-ab31-710374c6853d');
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('4aaca11b-cffd-4004-beb9-baa9a92605a5', 'zouliang', 'zouliang', '', '', '', '2026-11-17 17:49:27.118212+08', '11111111-1111-4111-8111-111111111111', 'b48e3adb-2208-4377-ab31-710374c6853d');
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('77e903b8-18b1-4f5d-b441-e3162a245da8', '志达', 'yuanzhida', '', '', '', '2026-11-19 11:28:34.726997+08', '74e6a23f-c81b-465a-a015-157d8759235c', NULL);
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('7975da6a-ee27-49c8-b7d1-f30bf699b95e', '文婷', 'wenting', '', '', '', '2026-11-19 11:30:43.129584+08', '74e6a23f-c81b-465a-a015-157d8759235c', NULL);
INSERT INTO public.people (id, name, username, email, phone, title, created_at, tenant_id, role_id) VALUES ('0a898660-7eeb-4cb5-aa94-1ca51f95c78f', '邹亮', 'zouliang', '', '', '前端开发', '2026-11-19 11:27:58.405774+08', '74e6a23f-c81b-465a-a015-157d8759235c', '384ff499-9d5c-43e2-b11b-fc6ac7a4f18f');


--
-- Data for Name: projects; Type: TABLE DATA; Schema: team; Owner: -
--

INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('cef27e90-d277-4bd4-a232-4c14fd957a43', 'web', '景云销-web', '', '2026-11-16 15:39:58.157046+08', '', '', '11111111-1111-4111-8111-111111111111', NULL, '');
INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('bc196c7f-ba75-44b3-a817-2606903e9878', 'default', '默认项目', '项目化迁移时生成的默认项目', '2026-11-16 14:46:05.501425+08', '', '', '11111111-1111-4111-8111-111111111111', NULL, '');
INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('e709035e-3197-41e3-b6a4-98b52d865da0', 'web', '景云销-web', '', '2026-11-18 19:51:12.002659+08', '', '0acc7952-73f1-40d7-abb5-79c3253dc010', '86128ddb-bd67-4d9d-b5db-6c233a80920f', NULL, '');
INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('159c14e6-bb8c-4891-b058-be8b31d832e8', 'app', '景云销-app', '', '2026-11-18 19:57:10.855658+08', '', '519ec15a-05b2-4e15-9597-cb93a76aaeb2', '86128ddb-bd67-4d9d-b5db-6c233a80920f', NULL, '');
INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('46aef893-3e67-4ccc-adce-3b4f34bb46c2', 'world2-1', '世界第二大公司第一个项目', '', '2026-11-19 11:26:27.922753+08', '', 'e6a404da-b42f-4ece-a402-be84f18d92d8', '74e6a23f-c81b-465a-a015-157d8759235c', NULL, '');
INSERT INTO team.projects (id, key, name, description, created_at, repo_url, owner_user_id, tenant_id, role_id, icon_url) VALUES ('00458584-c418-49f5-9945-0d8ac9a17808', 'world2-2', '世界第二大公司的第二个项目', '', '2026-11-19 11:26:50.139045+08', '', 'e6a404da-b42f-4ece-a402-be84f18d92d8', '74e6a23f-c81b-465a-a015-157d8759235c', NULL, '');


--
-- Data for Name: project_members; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.project_members (project_id, person_id) VALUES ('bc196c7f-ba75-44b3-a817-2606903e9878', 'eed39ad6-a992-43a8-b34c-29b0c6e9e6ba');
INSERT INTO public.project_members (project_id, person_id) VALUES ('159c14e6-bb8c-4891-b058-be8b31d832e8', '7bb2b732-9ea9-4e97-bf68-8745fade0e0b');
INSERT INTO public.project_members (project_id, person_id) VALUES ('e709035e-3197-41e3-b6a4-98b52d865da0', '7bb2b732-9ea9-4e97-bf68-8745fade0e0b');
INSERT INTO public.project_members (project_id, person_id) VALUES ('46aef893-3e67-4ccc-adce-3b4f34bb46c2', '0a898660-7eeb-4cb5-aa94-1ca51f95c78f');
INSERT INTO public.project_members (project_id, person_id) VALUES ('00458584-c418-49f5-9945-0d8ac9a17808', '77e903b8-18b1-4f5d-b441-e3162a245da8');
INSERT INTO public.project_members (project_id, person_id) VALUES ('46aef893-3e67-4ccc-adce-3b4f34bb46c2', 'b2393d84-6ba9-4034-847e-f6d8f2f2e50c');
INSERT INTO public.project_members (project_id, person_id) VALUES ('00458584-c418-49f5-9945-0d8ac9a17808', '7975da6a-ee27-49c8-b7d1-f30bf699b95e');


--
-- Data for Name: role_menus; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/admin/accounts');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/admin/audit-logs');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/directory/roles');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', '/settings');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', '/directory/projects');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/overview');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/packages');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/apps');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/directory/people');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/directory/teams');
INSERT INTO public.role_menus (role_id, menu_key) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', '/directory/projects');


--
-- Data for Name: role_permissions; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'role.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2d1c135f-c774-4a44-bcee-8da2f605b1d7', 'role.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'project.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'project.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'project.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'project.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1a5d2308-400c-4896-87b8-821f9c035c3b', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0944fd56-f489-430c-b448-aa8fe3485c7a', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('e06e3a55-fac8-44bc-ab2d-b35d99c032d1', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('0f90f3c2-7e97-473d-b9bf-f0bcd9fc5ba0', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('816b3eda-71f6-4e39-a36c-d06f11f80aa3', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('2b134e68-ee59-4850-8235-0586df9bb4ef', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b06b73ff-9644-4b39-9419-797cc04ceefc', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('7e57d655-709c-4a60-a9fe-775d3ebec152', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('1607eb37-f77a-465f-9fcb-6a3687067dbd', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('b48e3adb-2208-4377-ab31-710374c6853d', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'account.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'audit.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'build.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'build.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'build.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'directory.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'project.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'project.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'project.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'role.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('384ff499-9d5c-43e2-b11b-fc6ac7a4f18f', 'role.write');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'role.delete');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'role.read');
INSERT INTO public.role_permissions (role_id, permission) VALUES ('4312a819-b236-4bc5-8214-c8ac37e6ffd5', 'role.write');


--
-- Data for Name: system_settings; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.system_settings (id, webhook_enabled, webhook_url, webhook_secret, webhook_fields, api_key, updated_at, tenant_id, git_repo_path) VALUES (3, false, '', '', '["appName","version","buildNumber","environment","platform","downloadPageUrl"]', 'cd139c88ee07ffe657ba68d86a7a3beb117d6958292bb75415ff8e2e899b7564', '2026-11-19 19:52:40.033256+08', '74e6a23f-c81b-465a-a015-157d8759235c', '');
INSERT INTO public.system_settings (id, webhook_enabled, webhook_url, webhook_secret, webhook_fields, api_key, updated_at, tenant_id, git_repo_path) VALUES (1, false, '', '', '["appName","version","buildNumber","environment","platform","downloadPageUrl"]', '6463b345b6d96afe63dbb40c06bcf27da6d739bc9d9e723fe4ed9fd1e1762d27', '2026-11-25 10:42:31.626601+08', NULL, '');
INSERT INTO public.system_settings (id, webhook_enabled, webhook_url, webhook_secret, webhook_fields, api_key, updated_at, tenant_id, git_repo_path) VALUES (2, false, '', '', '["appName","version","buildNumber","environment","platform","downloadPageUrl"]', '', '2026-11-18 17:25:04.59233+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f', '');


--
-- Data for Name: teams; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.teams (id, name, description, created_at, tenant_id) VALUES ('a2c947f1-41db-4fa5-b096-ef43d7c17441', '世界第二大公司的第一个团队', '', '2026-11-19 11:27:14.700667+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.teams (id, name, description, created_at, tenant_id) VALUES ('e283855a-e2ad-4d82-92bf-3cc4ae514a85', '世界第二大公司的第二个团队', '', '2026-11-19 11:27:34.837267+08', '74e6a23f-c81b-465a-a015-157d8759235c');
INSERT INTO public.teams (id, name, description, created_at, tenant_id) VALUES ('ef4fd9f4-a16a-4862-8a22-c45d63fa9efb', '景云销-团队', '', '2026-11-16 15:41:19.058005+08', '11111111-1111-4111-8111-111111111111');
INSERT INTO public.teams (id, name, description, created_at, tenant_id) VALUES ('98382705-1a03-4d35-b4fa-824a8163923c', '景云销团队', 'web', '2026-11-18 19:50:10.141312+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');
INSERT INTO public.teams (id, name, description, created_at, tenant_id) VALUES ('09cf6fe1-ae7a-408f-a758-8e8e399b513f', '自己建设团队1', '', '2026-11-18 19:56:30.51647+08', '86128ddb-bd67-4d9d-b5db-6c233a80920f');


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('ac60c4e9-4730-403a-a3fa-93fc1805fdbb', 'chendong1', 'pbkdf2-sha256$100000$uyLFeuO3rSxppDDssU3zTg$bFghHYBmD6RXvRUscHDLW2sRw6rlIBcl9zRJil3WYYc', 'cb7f9a03-af72-49ab-bf44-3bfa1778d5cb', false, 'active', 1, NULL, '', '2026-11-19 15:09:51.538895+08', 'team', NULL, 'member');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('aff2b9a5-43c0-4d6d-92ab-16037f245219', 'zouliang', 'pbkdf2-sha256$100000$NYIfxztYv1Ux8MGaLVKKGA$JbvySub94uRz6jJMueBNHC/U+56ng3SczjCRvV9/gSw', '0a898660-7eeb-4cb5-aa94-1ca51f95c78f', false, 'active', 1, '2026-11-19 19:40:39.683234+08', '127.0.0.1', '2026-11-19 11:31:26.879137+08', 'team', '74e6a23f-c81b-465a-a015-157d8759235c', 'member');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('d0cd49a9-b1d2-4d7f-95e4-1da3ecff1828', 'admin', 'pbkdf2-sha256$100000$3xgXgxpyXk1xxo6FJmgitA$Pc/qMQwoSJ7jgBShJ2qMS9XfqtkJqYwEzmsxFPf5zXk', NULL, true, 'active', 3, '2026-11-25 14:20:45.055162+08', '134.195.101.193', '2026-11-16 16:39:30.480254+08', 'team', NULL, 'super_admin');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('f189ad6f-578d-4e9f-bb7e-633050d85581', 'xuzhijun', 'pbkdf2-sha256$100000$mV9YxfFlZ3OaazYECsTsYw$qvM+Gj/sPW++KTTv3QpIcfin6RA2ZmJkrJablEvY/nM', 'b2393d84-6ba9-4034-847e-f6d8f2f2e50c', false, 'active', 1, '2026-11-19 16:08:32.152018+08', '::1', '2026-11-19 11:31:51.278074+08', 'team', '74e6a23f-c81b-465a-a015-157d8759235c', 'member');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('e6a404da-b42f-4ece-a402-be84f18d92d8', 'worldCompany2', 'pbkdf2-sha256$100000$OAJ2uuoMx87eTm6C0plqXw$zrDN5Q9lahi0t+JzFjeNuiItLhhZK5dK74o75YuN1To', NULL, false, 'active', 1, '2026-11-25 14:23:40.891552+08', '134.195.101.193', '2026-11-19 10:53:49.824307+08', 'team', '74e6a23f-c81b-465a-a015-157d8759235c', 'platform_admin');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('48975c44-9767-404a-80b0-aaaf722467f2', 'linanan', 'pbkdf2-sha256$100000$npIlGmbCuAYf0xdbxx+zww$qU1WVLEM7tbMylGAD7ngCHKV5CZVDzYaqglPoR+woic', NULL, false, 'active', 1, '2026-11-17 18:34:05.169046+08', '::1', '2026-11-17 18:29:05.157385+08', 'team', '11111111-1111-4111-8111-111111111111', 'member');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('0acc7952-73f1-40d7-abb5-79c3253dc010', 'wordCompany1', 'pbkdf2-sha256$100000$a9dbfNxmRNeP+pH1+50L0A$taNUTJ0WH4MbrxRklLC0dCVIR+NxUVyWPr90vMoeaHE', 'cb7f9a03-af72-49ab-bf44-3bfa1778d5cb', false, 'active', 1, '2026-11-18 20:40:14.900598+08', '127.0.0.1', '2026-11-18 17:11:12.837186+08', 'team', '86128ddb-bd67-4d9d-b5db-6c233a80920f', 'platform_admin');
INSERT INTO public.users (id, username, password_hash, person_id, is_super_admin, status, token_version, last_login_at, last_login_ip, created_at, mode, tenant_id, account_level) VALUES ('519ec15a-05b2-4e15-9597-cb93a76aaeb2', 'chendong', 'pbkdf2-sha256$100000$3WthhMP0yM/4wffD9ZQVcw$VsqEJnaDtq0TA8bAjqH/5SCF3QQ8SAOvk5YafHzP9Y8', 'cb7f9a03-af72-49ab-bf44-3bfa1778d5cb', false, 'active', 1, '2026-11-18 19:55:13.2058+08', '::1', '2026-11-18 19:53:10.763608+08', 'team', '86128ddb-bd67-4d9d-b5db-6c233a80920f', 'member');


--
-- Data for Name: team_admins; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: team_members; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.team_members (team_id, person_id) VALUES ('ef4fd9f4-a16a-4862-8a22-c45d63fa9efb', '4aaca11b-cffd-4004-beb9-baa9a92605a5');
INSERT INTO public.team_members (team_id, person_id) VALUES ('98382705-1a03-4d35-b4fa-824a8163923c', 'cb7f9a03-af72-49ab-bf44-3bfa1778d5cb');
INSERT INTO public.team_members (team_id, person_id) VALUES ('98382705-1a03-4d35-b4fa-824a8163923c', 'e7459483-a42a-4441-aca4-198d4282af70');
INSERT INTO public.team_members (team_id, person_id) VALUES ('09cf6fe1-ae7a-408f-a758-8e8e399b513f', 'e7459483-a42a-4441-aca4-198d4282af70');
INSERT INTO public.team_members (team_id, person_id) VALUES ('09cf6fe1-ae7a-408f-a758-8e8e399b513f', '7bb2b732-9ea9-4e97-bf68-8745fade0e0b');
INSERT INTO public.team_members (team_id, person_id) VALUES ('a2c947f1-41db-4fa5-b096-ef43d7c17441', '0a898660-7eeb-4cb5-aa94-1ca51f95c78f');
INSERT INTO public.team_members (team_id, person_id) VALUES ('e283855a-e2ad-4d82-92bf-3cc4ae514a85', '77e903b8-18b1-4f5d-b441-e3162a245da8');
INSERT INTO public.team_members (team_id, person_id) VALUES ('a2c947f1-41db-4fa5-b096-ef43d7c17441', 'b2393d84-6ba9-4034-847e-f6d8f2f2e50c');
INSERT INTO public.team_members (team_id, person_id) VALUES ('e283855a-e2ad-4d82-92bf-3cc4ae514a85', '7975da6a-ee27-49c8-b7d1-f30bf699b95e');


--
-- Data for Name: team_projects; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.team_projects (team_id, project_id) VALUES ('ef4fd9f4-a16a-4862-8a22-c45d63fa9efb', 'cef27e90-d277-4bd4-a232-4c14fd957a43');
INSERT INTO public.team_projects (team_id, project_id) VALUES ('09cf6fe1-ae7a-408f-a758-8e8e399b513f', 'e709035e-3197-41e3-b6a4-98b52d865da0');
INSERT INTO public.team_projects (team_id, project_id) VALUES ('a2c947f1-41db-4fa5-b096-ef43d7c17441', '46aef893-3e67-4ccc-adce-3b4f34bb46c2');
INSERT INTO public.team_projects (team_id, project_id) VALUES ('e283855a-e2ad-4d82-92bf-3cc4ae514a85', '00458584-c418-49f5-9945-0d8ac9a17808');


--
-- Data for Name: projects; Type: TABLE DATA; Schema: solo; Owner: -
--

INSERT INTO solo.projects (id, key, name, description, repo_url, owner_user_id, created_at, tenant_id, role_id, icon_url) VALUES ('523359a5-ae5c-425d-80f2-75addb27cb3d', 'web', '景云销-web', '', '', 'd0cd49a9-b1d2-4d7f-95e4-1da3ecff1828', '2026-11-17 13:31:58.669336+08', '11111111-1111-4111-8111-111111111111', NULL, '');


--
-- Name: system_settings_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.system_settings_id_seq', 3, true);


--
-- PostgreSQL database dump complete
--

SET session_replication_role = DEFAULT;
