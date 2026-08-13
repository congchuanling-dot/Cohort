# Tool Failure Patterns

- raw_content_policy: omitted; args are represented only by args_hash when available
- sessions_scanned: 111
- tool_failure_count: 137

## code_run / status:error

- count: 49
- sessions: 20260726-211227-97509f11, 20260728-130030-890bf155, 20260728-132501-301cd808, 20260728-173349-204ab602, 20260805-174552-c3e23fde, 20260805-184043-5023b128, 20260805-192359-ba44b3e1, 20260807-174338-a4eb25a1, 20260812-061135-da7bdd0d, 20260812-061237-1cb511d6
- distinct_args_hashes: 28
- top_args_hashes: sha256:034efe3cc090a5367f0f9a393bb4332e7cb678bdc3db0a363b94353e6e3e8838=1, sha256:0afd36f3a483a7cd02fad41fb9b198cd4c1d3cb13674110e179ce84f9e01c614=1, sha256:0ed45a051e5f5d5a6b3a010b5a2bb0074e4011ae6bd79a684d7e59e616598a18=1, sha256:3508c6c2d9f873fad6aca395a933bbd67b68d55dd64363397ef45d511953b24c=1, sha256:3d446b5eafaf6bc6b5c39eac5e3db4bee06d9ea5ece4a26a8b4ef8764f118de7=1
- first_seen: 2026-07-26T13:21:04Z
- last_seen: 2026-08-11T22:13:01Z

## file_read / status:error

- count: 18
- sessions: 20260728-165613-c7a08a18, 20260730-120421-2a18b4e5, 20260730-120501-12ce0bc8, 20260807-174338-a4eb25a1
- distinct_args_hashes: 9
- top_args_hashes: sha256:43f163e4901c359914b765ed906baad5269cc57b5ca5e4b89520e2de012cd141=1, sha256:55bd310528639dacc8832623321cb31eacc2f08583c4c07d5eb51f96fb902876=1, sha256:74959b89309ed7dcb452f5b856805fa9a6b55d89dddc09258e7e979ea7f81553=1, sha256:763891b497df9de9be36fd7aa1708c933dec249a49d3d023c2bc13f7609daa2d=1, sha256:7d6441497d2a000b8143602a7817c90abe7db88e139f89c062a1c36cfe0ad9d6=1
- first_seen: 2026-07-28T08:59:38Z
- last_seen: 2026-08-07T09:47:40Z

## desktop_press_key / desktop_action_confirmation_required

- count: 11
- sessions: 20260726-205528-bd56d050, 20260726-210439-2b720f3f, 20260726-211227-97509f11, 20260728-132501-301cd808, 20260728-141957-b6b0dbd3, 20260728-173349-204ab602
- distinct_args_hashes: 10
- top_args_hashes: sha256:05abc7a7500170bce629ee84f59c23591289dbff248a839d34bdf75b1a59203a=1, sha256:0f73239415a42a4769a75f7fac5d9b06e3e97098079229c454a901ebabc80fd4=1, sha256:24b9c10a1ac94587aa1e30e1473a9513707829c35bf3b42c80af82c02b9a129d=1, sha256:519699fafc48a6f2b7a65b84992f2838f7ea095414602ff34f1b3f62abc30b42=1, sha256:5c94ccc0b26909e23c5770c1b6fddfbfddb5714c35362cfa2e650b80608fa5b5=1
- first_seen: 2026-07-26T12:58:55Z
- last_seen: 2026-07-28T09:36:27Z

## browser_execute_js / browser_error

- count: 8
- sessions: 20260729-152848-cc0c90fb, 20260805-184043-5023b128
- distinct_args_hashes: 4
- top_args_hashes: sha256:0405dcf1019b7b8f98f6253b698c787aaeb3f0cd0334115473ac6e78aa60cf63=1, sha256:427fb84ff648573caccf9a170d64231f3587514bc8aa114186915d2f186c9b28=1, sha256:617ba2cd0b0620878c5c98fd45b82cf8d875b9366187608756ed40eea9b09561=1, sha256:a2a531e04a81ab8666f379a93709fdf35cace8112aaa1860c4108ccf0e0654f4=1
- first_seen: 2026-07-29T07:29:55Z
- last_seen: 2026-08-05T11:15:37Z

## browser_ocr / browser_ocr_path_outside_workspace

- count: 8
- sessions: 20260805-174552-c3e23fde
- distinct_args_hashes: 4
- top_args_hashes: sha256:66ed5f3179b2ee172e2ea35de9394846dd6e6961b6b6e45bfe653f77797d4b35=1, sha256:6ef413495b676a07d74dd0f915ed46964a029126f0d3e0d3250431edb0822ad5=1, sha256:cc4895523096aaef2c1371470f25328702a26cfbf24e947ce98bb3986a330396=1, sha256:f892bc2110ce68c79515cb15e8f7155078306ea5c60328d42b497b3c252361f1=1
- first_seen: 2026-08-05T09:48:58Z
- last_seen: 2026-08-05T09:48:58Z

## browser_click_element / browser_error

- count: 7
- sessions: 20260728-145620-df94bc44, 20260728-165613-c7a08a18, 20260730-120501-12ce0bc8
- distinct_args_hashes: 4
- top_args_hashes: sha256:a8419e192dfa6af73724503cf0776476834e2bf0a230e77c0825f3184fa51af5=1, sha256:e54cd5dfa1bff02f31d5117eaec5077ed48dc300350b54a423214df298a539ab=1, sha256:e792c2dbebc027af2c49c74f6de9d33ad3166180b452e15406f9ee8b29eaebde=1, sha256:fd38430e0f5e91eefaba4d830dd4ad4d0b53d5be280d775957c935f267e1ce30=1
- first_seen: 2026-07-28T06:57:37Z
- last_seen: 2026-07-30T05:10:04Z

## computer_find / computer_find_state_required

- count: 4
- sessions: 20260726-203111-86483816, 20260726-205528-bd56d050, 20260726-211227-97509f11
- distinct_args_hashes: 4
- top_args_hashes: sha256:1ac7c7804e63e96f3e0c0aa02b280fbf5200a1b5cd4ad41aab4bfe15e9f9708a=1, sha256:928e38beed623a5c1907e266cd4d6b4dff8cd5ea1001e7774a3feed59f537723=1, sha256:9a3b62dec0e32f8b4357d6b081dedcfbc86393746b9d887f48a938e70bd8f7fd=1, sha256:f53b5e0811e4b0158d995c12f421435f3813eef8f19083d8b63422474f28f615=1
- first_seen: 2026-07-26T12:35:53Z
- last_seen: 2026-07-26T13:15:22Z

## computer_wait / computer_wait_timeout

- count: 3
- sessions: 20260726-203111-86483816, 20260728-120252-c6842d26, 20260728-130030-890bf155
- distinct_args_hashes: 3
- top_args_hashes: sha256:3ac4bdeaaa2ed934c1f9b6f0c809fb0680b92771469ce98b046a1996e61aa71d=1, sha256:e056f514fb4313fc2b3289704f220f8e7cbaf1dbab1c6deabad350c6183ebe07=1, sha256:f21515a7aabf03fcfb8d158a9667bd0a33e8bff000e6cdae5764403872476ab3=1
- first_seen: 2026-07-26T12:36:34Z
- last_seen: 2026-07-28T05:02:21Z

## ask_user / confirmation_bad_request

- count: 2
- sessions: 20260730-120501-12ce0bc8
- distinct_args_hashes: 1
- top_args_hashes: sha256:b39dd6a027176941d6d39de63ca0aa96bb871e54403261b7666d22decd2c6f27=1
- first_seen: 2026-07-30T05:15:20Z
- last_seen: 2026-07-30T05:15:20Z

## browser_execute_js / browser_bad_script

- count: 2
- sessions: 20260805-192359-ba44b3e1
- distinct_args_hashes: 1
- top_args_hashes: sha256:71267cc4625875dd8a317dad93f98e58dfa88c465485a62e702c2bea53fe9dcb=1
- first_seen: 2026-08-05T11:33:45Z
- last_seen: 2026-08-05T11:33:45Z

## browser_open / browser_error

- count: 2
- sessions: 20260805-192359-ba44b3e1
- distinct_args_hashes: 1
- top_args_hashes: sha256:6f4daef30deb806a88832b25e0e5422933b8f198f50564e52d9e5be20b796d03=1
- first_seen: 2026-08-05T11:24:50Z
- last_seen: 2026-08-05T11:24:50Z

## browser_open / browser_not_connected

- count: 2
- sessions: 20260730-120501-12ce0bc8
- distinct_args_hashes: 1
- top_args_hashes: sha256:3d68618bd71a025bf1555f0ef36bb0db59770c64b0ebd0c645a12a4540dc862e=1
- first_seen: 2026-07-30T04:05:41Z
- last_seen: 2026-07-30T04:05:41Z

## browser_open / tool_run_failed

- count: 2
- sessions: 20260805-123728-ce03479a
- distinct_args_hashes: 1
- top_args_hashes: sha256:7134e978d1398625814e0d178a8109f0776bbfb8b8ee40a36336379a5a28a18b=1
- first_seen: 2026-08-05T04:37:56Z
- last_seen: 2026-08-05T04:37:56Z

## browser_scan / browser_error

- count: 2
- sessions: 20260805-192359-ba44b3e1
- distinct_args_hashes: 1
- top_args_hashes: sha256:7e90ade1a37b7eb3f3ee0c21257189403da6656a9ff8b2b7dc6bfa363c825802=1
- first_seen: 2026-08-05T11:24:46Z
- last_seen: 2026-08-05T11:24:46Z

## computer_press / desktop_action_confirmation_required

- count: 2
- sessions: 20260726-203111-86483816, 20260728-130030-890bf155
- distinct_args_hashes: 2
- top_args_hashes: sha256:23bd24b0098a2ef196aa971a578effd2164663d2dcde22698587ddf70b3290da=1, sha256:8109a8943069673c0ef068ed4e4bd65401598e8b7490342e0d868eb2aed6defd=1
- first_seen: 2026-07-26T12:43:27Z
- last_seen: 2026-07-28T05:01:57Z

## computer_wait / computer_wait_no_target_window

- count: 2
- sessions: 20260805-124500-4affa496
- distinct_args_hashes: 1
- top_args_hashes: sha256:f49b38a0d94025e254eb9a8f01c5ab4ce4235243459f80be8a322fdb00432e86=1
- first_seen: 2026-08-05T04:45:21Z
- last_seen: 2026-08-05T04:45:21Z

## desktop_screenshot / desktop_target_not_active

- count: 2
- sessions: 20260728-173349-204ab602
- distinct_args_hashes: 1
- top_args_hashes: sha256:043d08c98101431c855d35d35ead4ba182747d01292b78906c668d6e56a92515=1
- first_seen: 2026-07-28T09:37:09Z
- last_seen: 2026-07-28T09:37:09Z

## mcp_filesystem_directory_tree / status:error

- count: 2
- sessions: 20260807-174338-a4eb25a1
- distinct_args_hashes: 1
- top_args_hashes: sha256:cac8adb6383e28c4cd5d1c1684c048ed3eecd09bf4b6bfbfc371d30271698e04=1
- first_seen: 2026-08-07T09:47:33Z
- last_seen: 2026-08-07T09:47:33Z

## sop_read / tool_run_failed

- count: 2
- sessions: 20260805-124500-4affa496
- distinct_args_hashes: 1
- top_args_hashes: sha256:cf6a70b29296f8371226d4c5959dfe00ce1e348853a1dd7e6befc95864530993=1
- first_seen: 2026-08-05T04:45:04Z
- last_seen: 2026-08-05T04:45:04Z

## computer_check / computer_check_state_required

- count: 1
- sessions: 20260726-205528-bd56d050
- distinct_args_hashes: 1
- top_args_hashes: sha256:2a910fd8943b762b9426dec1156f877a018218f84181e1f6de28e10893a521d3=1
- first_seen: 2026-07-26T12:58:38Z
- last_seen: 2026-07-26T12:58:38Z

## computer_click / desktop_action_confirmation_required

- count: 1
- sessions: 20260726-203111-86483816
- distinct_args_hashes: 1
- top_args_hashes: sha256:fbcb6d123157a91b342ea52514d518b4a3adb47a505ab17101feff0c8f06ec31=1
- first_seen: 2026-07-26T12:40:16Z
- last_seen: 2026-07-26T12:40:16Z

## computer_press / computer_press_state_required

- count: 1
- sessions: 20260726-203111-86483816
- distinct_args_hashes: 1
- top_args_hashes: sha256:cada5e7380cfd27b601ab5ac7edca3ef49592386264f814f7b3187740f732932=1
- first_seen: 2026-07-26T12:32:52Z
- last_seen: 2026-07-26T12:32:52Z

## computer_scroll / computer_scroll_state_required

- count: 1
- sessions: 20260728-145620-df94bc44
- distinct_args_hashes: 1
- top_args_hashes: sha256:5cfdf5b45b775a94d98ab4e6bbad8e2e5d8f8f75f492d47e84fb7216246a8627=1
- first_seen: 2026-07-28T06:56:57Z
- last_seen: 2026-07-28T06:56:57Z

## computer_visual_snapshot / computer_visual_snapshot_no_observation

- count: 1
- sessions: 20260728-130030-890bf155
- distinct_args_hashes: 1
- top_args_hashes: sha256:641e0c1c1a02ad04a15373b4bef4753627ce2c1ec2e9ebf8df57849d3e04eb9c=1
- first_seen: 2026-07-28T05:01:14Z
- last_seen: 2026-07-28T05:01:14Z

## desktop_visual_click / desktop_action_confirmation_required

- count: 1
- sessions: 20260726-211227-97509f11
- distinct_args_hashes: 1
- top_args_hashes: sha256:1dbb4fc9ee344a4fabab818c4e8095c217b68173457db1ba79447aa0fd33f1ab=1
- first_seen: 2026-07-26T13:22:01Z
- last_seen: 2026-07-26T13:22:01Z

## mcp_filesystem_search_files / status:error

- count: 1
- sessions: 20260727-203206-aece2785
- distinct_args_hashes: 1
- top_args_hashes: sha256:0608ea40c16452f01ded9fc9a19b762fed0ebbe809a466be7a5529a08ff1a916=1
- first_seen: 2026-07-27T12:32:40Z
- last_seen: 2026-07-27T12:32:40Z

