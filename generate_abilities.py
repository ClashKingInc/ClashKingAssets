"""Generate player-facing equipment and Builder Base abilities from parsed game tables.

The owning catalog supplies IDs and exclusions. Shared mechanic rules select stats;
unknown effect fields fail generation. Spatial values retain the static-data scale.
No game downloads or repository reads occur at import time.
"""

import json
import re
from collections import Counter
from pathlib import Path

FILES = ('special_abilities', 'character_items', 'characters', 'spells', 'projectiles', 'heroes')


def seconds(ms):
    return ms / 1000


def snake(name):
    return re.sub('([a-z0-9])([A-Z])', '\\1_\\2', name).lower()


ROOT_RULES = {
    'Damage': ('damage', None),
    'MinDamage': ('min_damage', None),
    'DPS': ('dps', None),
    'DamageRadius': ('damage_radius', None),
    'AttackRange': ('attack_range', None),
    'AttackSpeed': ('attack_speed', None),
    'ExtraDamageFlat': ('extra_damage', None),
    'BoostDamagePercentage': ('damage_increase_percent', None),
    'BoostAttackSpeedPercentage': ('attack_speed_increase_percent', None),
    'ShieldProtectionPercent': ('damage_reduction_percent', None),
    'PercentHealthIncrease': ('health_increase_percent', None),
    'DamagePermilHp': ('damage_percent_target_health', lambda n: n / 10),
    'SpeedBoost': ('movement_speed_increase', None),
    'FrostOnHitPercent': ('slowdown_percent', None),
    'FrostOnHitTime': ('slowdown_duration', seconds),
    'StunOnHitTime': ('stun_duration', seconds),
    'HealOnActivation': ('healing', None),
    'ReflectedDamage': ('reflected_damage', None),
    'HealOnReflect': ('healing_per_reflection', None),
    'ReflectCooldownMS': ('reflection_cooldown', seconds),
    'ActAsPenetratingDamage': ('damage', None),
    'ActAsPenetratingRadius': ('damage_radius', None),
    'PenetratingRadius': ('projectile_radius', None),
    'PenetratingExtraRange': ('projectile_range', None),
    'ActiveAfterTakingDamage': ('damage_taken_per_activation', None),
    'ActiveAfterFriendlyHousingSpaceDied': ('housing_space_lost_required', None),
    'DeactivateAfterHousingSpaceDeployed': ('deactivates_at_housing_space', None),
    'ExtraDamagePercentageAgainstTarget': ('target_damage_multiplier_percent', None),
    'ChainAttackDepth': ('chain_targets', None),
    'ChainAttackDamageReductionPercent': ('damage_loss_per_chain_percent', None),
    # Recall stats describe the recalled allies, not the equipment owner's
    # ordinary movement or healing. Keep their scope and source spatial scale.
    'RecallSpeedBoost': ('recall_speed_boost', None),
    'RecallHealPercent': ('recall_healing_percent', None),
    'RecallHousing': ('recall_housing_space', None),
    'RecallSpeedBoostTimeMS': ('recall_speed_boost_duration', seconds),
    'RecallTroopsRadius': ('recall_radius', None),
}

ROOT_BOOLEAN_RULES = {
    'Untargetable': 'untargetable',
    'ImmuneToFriendlySpells': 'immune_to_friendly_spells',
}

ROOT_CONTROL = {
    'Name',
    'Level',
    'TID',
    'InfoTID',
    'InfoScreenAttribute1',
    'InfoScreenAttribute2',
    'InfoScreenAttribute3',
    'InfoScreenAttribute1Divider',
    'InfoScreenAttribute2Divider',
    'InfoScreenAttribute3Divider',
    'GivenAbility',
    'GivenAbilityLevel',
    'GiveAbilityToSpawnedTroop',
    'SpawnedTroop',
    'TroopLevel',
    'TroopCount',
    'SpawnnedTroopsPerHit',
    'SpawnDelayBetweenHitsMS',
    'SpawnPattern',
    'CopySpawnnedTroopLevelFromAvatar',
    'AuraSpell',
    'AuraSpellLevel',
    'SelfSpell',
    'SelfSpellLevel',
    'CastSpell',
    'CastSpellLevel',
    'CastSpellTarget',
    'Projectile',
    'ProjectileOnActivation',
    'ProjectileOnActivationCopyTarget',
    'ProjectileOnActivationCount',
    'ProjectileOnActivationDifferentTargets',
    'ProjectileBounces',
    'ActiveOnDeath',
    'ActiveAfterPlayerInput',
    'ActiveAfterPlaceCommand',
    'ActiveWhileAlive',
    'ActiveWhileAloneRadius',
    'DeactivateWhileNotAloneRadius',
    'DeactivateAfterTime',
    'DeactivateAfterNumberOfHits',
    'DeactivateAfterPlayerInput',
    'ActivationCooldown',
    'MaxActivations',
    'SimulatePlayerInputOnDeath',
    'DeactivateAfterMasterDies',
    'DeactivateOnDeath',
    'DeactivateWhenAnyCriteriaMet',
    'ActiveAfterFriendlyHousingSpaceDied',
    'ActiveAfterTakingDamage',
    'Regeneration',
    'RegenerationTimeBetweenHitsMS',
    'SelfDamagePerHit',
    'IsInvisible',
    'IsJumper',
    'Speed',
    'SelfDestruct',
    'HealthOverride',
    'DisableDefaultEquipment',
    'DieDamage',
    'ExtraDamageTarget',
    'ChainShootingDistance',
    'ChainAttackDistance',
    'ChainAttackFactor',
    'ChainAttackAllowMultipleChains',
    'ChainAttackKeepOriginalDamageForInitialHit',
    'ChainAttackDelay',
    'IncreasingDamage',
    'CreateAoEFromRange',
    'PreferedTargetBuildingClass',
    'DisableAttacking',
    'DisableRetargeting',
    'FightWithGroups',
    'Priority',
    'MovementOverrideDistance',
    'MovementOverrideTarget',
    'ActAsPenetratingProjectile',
    'ActAsPenetratingAirTargets',
    'ActAsPenetratingGroundTargets',
    'ReflectsDamage',
    'ReflectedRange',
    'ReflectBehavior',
    'ReflectProjectile',
    'TargetKilledCooldownTimer',
    'TriggerCooldownOnDeactivation',
    'IsPlaceholder',
    'SpellsBoostedByParent',
    'OverrideStatBars',
    'GrowthScale',
    'JumpHeightPercentage',
    'SmoothJump',
    'PreActivationDelayTime',
    'PreDeactivationDelayTime',
}

GRAPHICS = re.compile(
    'Effect(?:\\d+)?$|EffectOffset$|Effect2Delay$|^Animation|^Icon|^BigPicture|ClipName$|Sound$|SWF$|^ProjectileVisOverride$'
)

SPELL_FIELDS = set(
    (
        'BoostTimeMS BuildingDamagePermil ChargingTimeMS ConeAngle Damage DamageBoostPercent DeployTimeMS '
        'DestroyWalls DisableDonate DisableProduction DoesNotAffectOwner ExecuteHealthPermil '
        'ExtraHealthMax ExtraHealthMin ExtraHealthPermil FreezeOuterTimeMS FreezeTimeMS GlobalID '
        'HeroDamageMultiplier HitTimeMS ImmunityHeroes ImmunityOtherBuildings ImmunityOtherCharacters '
        'ImmunitySiegeMachines ImmunityStorages ImmunityTH_CC ImmunityTotems ImmunityWalls InfoTID Level '
        'MinDamage Name NoClipping NumberOfHits Radius RandomRadius RandomRadiusAffectsOnlyGfx '
        'ScaleDeployEffects ShieldProtectionPercent ShieldTime SpawnDuration SpawnFirstGroupSize '
        'SpawnUpgradeLevel SpeedBoost SpeedBoost2 StrengthWeight StunTimeMS SummonTroop TID '
        'TargetInfoString TimeBetweenHitsMS TroopDamagePermil UnitsToSpawn VillageType'
    ).split()
)

PROJECTILE_FIELDS = set(
    (
        'AllowSkinOverride BallisticHeight DamageDelay DepthBiasBack DepthBiasFront DirectionCount '
        'DirectionFrame DontTrackTarget ExportName FixedTravelTime HitSpell HitSpellInheritAffectType '
        'HitSpellLevel HitsGroundAndAir IsBallistic MaxBounceDistance MaxHitBuildings MaxHitObjects '
        'MaxTravelTime Name ParticleEmitter PenetratingHitBoxWidth PlayOnce PullTarget '
        'RandomHitPositionOnCharacters RetargetRadius RetargetTimer ReturningProjectile RotateEmitter '
        'Scale ScaleTimeline ShadowExportName SlowdownDefencePercent SmallRandomHitPositionOnBuildings '
        'SmoothDamage Speed SpellsBoostedByParent StartHeight StartOffset StartOffsetFromGunBone '
        'StopToTargetTime TargetPosRandomRadius TrajectoryStyle UseNormalizeLenghtFix '
        'UseParentAnimation3D UseRotate UseTopLayer'
    ).split()
)


class AbilitySourceResolver:
    def __init__(self, tables, texts, catalog):
        self.tables = tables
        self.texts = texts
        self.catalog = catalog
        self.issues = []
        self.counts = Counter()

    def resolve(self, table, name, level):
        record = self.tables[table][name]
        result = {k: v for k, v in record.items() if not isinstance(v, dict)}
        if level is not None and any((isinstance(v, dict) for v in record.values())):
            if str(level) not in record:
                raise ValueError(f'Missing referenced level: {table}/{name}/{level}')
            result.update(record[str(level)])
        return result

    def meta(self, data):
        tids = {'name': data.get('TID'), 'info': data.get('InfoTID')}
        parameters = {}
        for number in range(1, 4):
            field = f'InfoScreenAttribute{number}'
            if field in data:
                divider = data.get(field + 'Divider', 1)
                parameters['num' if number == 1 else f'num{number}'] = {
                    'raw_value': data[field],
                    'divider': divider,
                    'value': data[field] / divider if divider else None,
                    'source_field': field,
                }
        return {
            'name': self.texts.get(tids['name'], {}).get('EN'),
            'info': self.texts.get(tids['info'], {}).get('EN'),
            'TID': tids,
            'info_parameters': parameters,
        }

    def linked_nodes(self, table, name, level, data, ancestors=()):
        nodes = []
        for field, value in data.items():
            if not isinstance(value, str) or not re.search('Spell|Ability|Projectile|Troop|Character', field):
                continue
            if field in ('SpecialAbilities', 'MainAbilities', 'ExtraAbilities', 'AllowedCharacters'):
                continue
            for target_name in value.split(';'):
                candidates = [t for t in FILES if target_name in self.tables[t]]
                if not candidates:
                    continue
                expected = (
                    'spells'
                    if 'Spell' in field
                    else 'special_abilities'
                    if 'Ability' in field
                    else 'projectiles'
                    if 'Projectile' in field
                    else None
                )
                if expected in candidates:
                    candidates = [expected]
                if len(candidates) != 1:
                    self.issues.append(
                        {
                            'kind': 'ambiguous_reference',
                            'source': [table, name, level],
                            'field': field,
                            'target': target_name,
                            'candidates': candidates,
                        }
                    )
                    nodes.append(
                        {
                            'via': field,
                            'source_name': target_name,
                            'status': 'ambiguous',
                            'candidate_tables': candidates,
                        }
                    )
                    continue
                target_table = candidates[0]
                target_level = data.get(field + 'Level')
                level_source = field + 'Level' if target_level is not None else None
                if field == 'SummonTroop':
                    level_source = 'SpawnUpgradeLevel' if table == 'spells' else 'SummonLevel'
                    target_level = data.get(level_source)
                if field == 'SpawnedTroop':
                    level_source = 'TroopLevel'
                    target_level = data.get(level_source)
                    if data.get('CopySpawnnedTroopLevelFromAvatar'):
                        target_level, level_source = (None, 'CopySpawnnedTroopLevelFromAvatar')
                if target_level is not None:
                    target_level = int(target_level)
                key = (target_table, target_name, target_level)
                node = {
                    'via': field,
                    'source_file': f'logic/{target_table}.csv',
                    'source_name': target_name,
                    'level': target_level,
                    'level_source': level_source,
                }
                if key in ancestors:
                    node['status'] = 'cycle_reference'
                elif len(ancestors) >= 6:
                    node['status'] = 'depth_limit'
                    self.issues.append({'kind': 'depth_limit', 'source': list(key)})
                else:
                    props = self.resolve(target_table, target_name, target_level)
                    node.update(self.meta(props))
                    node['properties'] = props
                    node['level_fields'] = list(self.tables[target_table][target_name].get(str(target_level), {}))
                    node['status'] = (
                        'resolved'
                        if target_level is not None
                        or not any((isinstance(v, dict) for v in self.tables[target_table][target_name].values()))
                        else 'requires_runtime_level'
                    )
                    node['references'] = self.linked_nodes(
                        target_table, target_name, target_level, props, (*ancestors, key)
                    )
                    if props.get('SpecialAbilities') and props.get('SpecialAbilitiesLevel') is not None:
                        node['abilities'] = self.ability_list(
                            props, 'SpecialAbilities', 'SpecialAbilitiesLevel', (*ancestors, key)
                        )
                    elif props.get('SpecialAbilities'):
                        node['ability_resolution'] = 'requires_runtime_level'
                self.counts['references'] += 1
                nodes.append(node)
        return nodes

    def ability_list(self, owner, names_key, levels_key, ancestors=()):
        names = [v for v in str(owner.get(names_key, '')).split(';') if v]
        levels = str(owner.get(levels_key, '')).split(';') if names else []
        if len(names) != len(levels) or any((not v for v in levels)):
            raise ValueError(f"Unpaired ability list: {owner.get('Name')} {names_key} {names} {levels}")
        entries = []
        for name, level in zip(names, levels):
            level = int(level)
            props = self.resolve('special_abilities', name, level)
            entry = {
                'source_name': name,
                'source_file': 'logic/special_abilities.csv',
                'source_role': names_key,
                'level': level,
                **self.meta(props),
                'properties': props,
            }
            entry['level_fields'] = list(self.tables['special_abilities'][name].get(str(level), {}))
            entry['references'] = self.linked_nodes(
                'special_abilities', name, level, props, (*ancestors, ('special_abilities', name, level))
            )
            if props.get('TID') and (not props.get('InfoTID')):
                self.counts['ability_occurrences_without_description'] += 1
            self.counts['ability_occurrences'] += 1
            entries.append(entry)
        return entries

    def build(self):
        groups = []
        for kind, table in [('equipment', 'character_items'), ('troops', 'characters')]:
            by_tid = {
                item['TID']['name']: item
                for item in self.catalog[kind]
                if kind == 'equipment' or item.get('village') == 'builderBase'
            }
            for name, source in self.tables[table].items():
                if (
                    source.get('TID') not in by_tid
                    or source.get('DisableProduction')
                    or source.get('Deprecated')
                    or name.startswith('UNUSED')
                ):
                    continue
                item = by_tid[source['TID']]
                rows = []
                current_names = {
                    key: source.get(key) for key in ('MainAbilities', 'ExtraAbilities', 'SpecialAbilities')
                }
                for level, row in sorted(((int(k), v) for k, v in source.items() if isinstance(v, dict))):
                    props = self.resolve(table, name, level)
                    for key in current_names:
                        if key in row:
                            current_names[key] = row[key]
                        if current_names[key] is not None:
                            props[key] = current_names[key]
                    entries = []
                    for key, level_key in [
                        ('MainAbilities', 'MainAbilityLevels'),
                        ('ExtraAbilities', 'ExtraAbilityLevels'),
                        ('SpecialAbilities', 'SpecialAbilitiesLevel'),
                    ]:
                        entries.extend(self.ability_list(props, key, level_key))
                    rows.append(
                        {
                            'level': level,
                            'item_properties': props,
                            'item_references': self.linked_nodes(table, name, level, props, ((table, name, level),)),
                            'abilities': entries,
                        }
                    )
                if not any((r['abilities'] for r in rows)):
                    continue
                groups.append(
                    {
                        'item_id': item['_id'],
                        'item_type': kind,
                        'name': item['name'],
                        'village': item.get('village', 'home'),
                        'TID': item['TID'],
                        'source_name': name,
                        'source_file': f'logic/{table}.csv',
                        'levels': rows,
                    }
                )
        groups.sort(key=lambda g: g['item_id'])
        if self.issues:
            raise ValueError(f'Unresolved ability references: {self.issues}')
        return groups


class AbilityGenerator:
    def __init__(self, tables, texts, catalog):
        self.texts = texts
        self.source = AbilitySourceResolver(tables, texts, catalog).build()
        self.known_ids = {
            i['TID']['name']: i['_id'] for category in ('troops', 'heroes') for i in catalog.get(category, [])
        }
        self.skipped = []
        self.trace = []
        self.derived = []

    def assert_known_root(self, node):
        unknown = [
            k
            for k in node.get('properties', {})
            if (
                k not in ROOT_RULES
                and k not in ROOT_BOOLEAN_RULES
                and k not in ROOT_CONTROL
                and not GRAPHICS.search(k)
            )
        ]
        if unknown:
            raise ValueError(f"Unclassified ability fields: {node['source_name']}: {unknown}")

    def unit_identity(self, node, source_name=None):
        p = node.get('properties', {})
        name = node.get('name') or source_name or node.get('source_name')
        unit = {'name': name}
        item_id = self.known_ids.get(p.get('TID')) or p.get('GlobalID')
        if item_id:
            unit['_id'] = item_id
        return unit

    def trigger(self, p):
        for field, value in [
            ('ActiveOnDeath', 'on_death'),
            ('ActiveAfterTakingDamage', 'on_damage_taken'),
            ('ActiveAfterFriendlyHousingSpaceDied', 'on_allies_defeated'),
            ('ActiveWhileAloneRadius', 'while_alone'),
            ('DeactivateAfterPlayerInput', 'toggle'),
            ('ActiveAfterPlayerInput', 'activated'),
            ('ActiveAfterPlaceCommand', 'on_deploy'),
            ('ActiveWhileAlive', 'ongoing'),
        ]:
            if p.get(field):
                return value
        if p.get('DeactivateAfterNumberOfHits'):
            return 'first_attacks'
        if p.get('FrostOnHitPercent') or p.get('SelfDamagePerHit'):
            return 'on_attack'
        return None

    def project(self, root, owner_type):
        self.assert_known_root(root)
        p = root['properties']
        if p.get('IsPlaceholder'):
            self.skipped.append({'key': root['source_name'], 'reason': 'mode_switch_placeholder'})
            return None
        values, origins = ({}, {})
        seen = set()

        def add(key, value, node, fields, transform='copy', replace=False):
            if key in values and values[key] != value and (not replace):
                raise ValueError(f"Conflicting effects in {root['source_name']}: {key}")
            values[key] = value
            origins[key] = {
                'source_file': node.get('source_file'),
                'source_name': node.get('source_name'),
                'source_level': node.get('level'),
                'fields': fields,
                'transform': transform,
            }
            if transform != 'copy':
                self.derived.append({'ability': root['source_name'], 'field': key, 'transform': transform})

        def visit(node, prefix='', role='root'):
            identity = (node.get('source_file'), node.get('source_name'), node.get('level'), prefix)
            if identity in seen:
                return
            seen.add(identity)
            q = node.get('properties', {})
            table = node.get('source_file')
            if node.get('status') in ('ambiguous', 'cycle_reference', 'depth_limit'):
                raise ValueError(f'Unresolved effect reference: {identity}')
            is_ability = table == 'logic/special_abilities.csv'
            is_spell = table == 'logic/spells.csv'
            is_character = table in ('logic/characters.csv', 'logic/heroes.csv')
            classified = SPELL_FIELDS if is_spell else PROJECTILE_FIELDS if table == 'logic/projectiles.csv' else None
            if classified is not None:
                unknown = [k for k in q if k not in classified and (not GRAPHICS.search(k))]
                if unknown:
                    raise ValueError(f"Unclassified effect fields: {node['source_name']}: {unknown}")
            if is_ability:
                self.assert_known_root(node)
                for field, (key, converter) in ROOT_RULES.items():
                    value = q.get(field)
                    if value is not None and (not isinstance(value, (int, float)) or isinstance(value, bool)):
                        raise ValueError(f"Unexpected type for {node['source_name']}/{field}: {type(value).__name__}")
                    if field == 'ExtraDamagePercentageAgainstTarget' and (not q.get('ExtraDamageTarget')):
                        continue
                    if isinstance(value, (int, float)) and value > 0:
                        add(
                            prefix + key,
                            converter(value) if converter else value,
                            node,
                            [field],
                            'milliseconds / 1000'
                            if converter is seconds
                            else 'per mille / 10'
                            if converter
                            else 'copy',
                        )
                for field, key in ROOT_BOOLEAN_RULES.items():
                    if field not in q:
                        continue
                    if not isinstance(q[field], bool):
                        raise ValueError(
                            f"Unexpected type for {node['source_name']}/{field}: {type(q[field]).__name__}"
                        )
                    add(prefix + key, q[field], node, [field])
                if q.get('HealthOverride', 0) > 0:
                    add(prefix + 'hitpoints', q['HealthOverride'], node, ['HealthOverride'], replace=True)
                if q.get('Regeneration', 0) > 0 and q.get('RegenerationTimeBetweenHitsMS', 0) > 0:
                    add(
                        prefix + 'healing_per_second',
                        q['Regeneration'] * 1000 / q['RegenerationTimeBetweenHitsMS'],
                        node,
                        ['Regeneration', 'RegenerationTimeBetweenHitsMS'],
                        'Regeneration * 1000 / RegenerationTimeBetweenHitsMS',
                    )
                if q.get('SelfDamagePerHit', 0) < 0:
                    add(
                        prefix + 'healing_per_attack',
                        -q['SelfDamagePerHit'],
                        node,
                        ['SelfDamagePerHit'],
                        'negative self damage becomes positive healing',
                    )
                if q.get('IsInvisible'):
                    add(prefix + 'invisible', True, node, ['IsInvisible'])
                    if q.get('DeactivateAfterTime', 0) > 1:
                        add(
                            prefix + 'invisibility_duration',
                            seconds(q['DeactivateAfterTime']),
                            node,
                            ['DeactivateAfterTime'],
                            'milliseconds / 1000',
                        )
                if q.get('IsJumper'):
                    add(prefix + 'jumps_walls', True, node, ['IsJumper'])
                if q.get('DeactivateAfterPlayerInput') and q.get('Speed') == 0:
                    add(prefix + 'movement_speed', 0, node, ['Speed'])
                duration = q.get('DeactivateAfterTime', 0)
                sustained = any(
                    (
                        q.get(k)
                        for k in (
                            'BoostDamagePercentage',
                            'ShieldProtectionPercent',
                            'SpeedBoost',
                            'IsJumper',
                            'SelfDestruct',
                            'AuraSpell',
                        )
                    )
                )
                if duration > 1 and sustained and (not q.get('DeactivateAfterPlayerInput')):
                    add(
                        prefix + ('lifetime' if q.get('SelfDestruct') else 'duration'),
                        seconds(duration),
                        node,
                        ['DeactivateAfterTime'],
                        'milliseconds / 1000',
                    )
                if q.get('ActivationCooldown', 0) > 0:
                    add(
                        prefix + 'cooldown',
                        seconds(q['ActivationCooldown']),
                        node,
                        ['ActivationCooldown'],
                        'milliseconds / 1000',
                    )
                if q.get('ActiveWhileAloneRadius', 0) > 0:
                    add(prefix + 'isolation_radius', q['ActiveWhileAloneRadius'], node, ['ActiveWhileAloneRadius'])
                if (
                    q.get('DeactivateAfterNumberOfHits', 0) > 0
                    and (not q.get('ProjectileOnActivation'))
                    and (not q.get('SelfSpell'))
                ):
                    add(
                        prefix + 'empowered_attacks',
                        q['DeactivateAfterNumberOfHits'],
                        node,
                        ['DeactivateAfterNumberOfHits'],
                    )
                if q.get('ProjectileBounces', 0) > 0:
                    if q.get('ProjectileOnActivationCopyTarget') and q.get('PreferedTargetBuildingClass'):
                        add(
                            prefix + 'targets_hit',
                            q['ProjectileBounces'] + 1,
                            node,
                            ['ProjectileBounces', 'ProjectileOnActivationCopyTarget'],
                            'building-target projectile: first target plus subsequent bounces',
                        )
                    elif q.get('ChainShootingDistance'):
                        add(
                            prefix + 'landings',
                            q['ProjectileBounces'],
                            node,
                            ['ProjectileBounces', 'ChainShootingDistance'],
                            'ground-bouncing projectile: source landing count',
                        )
                    else:
                        raise ValueError(f"Unknown bounce-count convention: {node['source_name']}")
                if q.get('ProjectileOnActivationDifferentTargets') and q.get('ProjectileOnActivationCount', 0) > 0:
                    add(prefix + 'targets_hit', q['ProjectileOnActivationCount'], node, ['ProjectileOnActivationCount'])
                if q.get('ExtraDamageTarget'):
                    add(prefix + 'bonus_damage_target', q['ExtraDamageTarget'], node, ['ExtraDamageTarget'])
                if (
                    owner_type == 'troops'
                    and role == 'root'
                    and q.get('ActiveAfterPlayerInput')
                    and (q.get('MaxActivations') == 1)
                ):
                    add('uses', 1, node, ['MaxActivations'])
                if q.get('SpellsBoostedByParent') and q.get('SelfSpell'):
                    add(prefix + 'affected_by_damage_boosts', True, node, ['SpellsBoostedByParent'])
            elif is_spell:
                damage = q.get('Damage', 0)
                # Healing auras expose their sustained rate, not implementation
                # tick damage, tick timing, or the per-tick health gate.
                sustained_healing = (
                    role == 'aura' and damage < 0 and q.get('NumberOfHits', 0) > 1 and q.get('TimeBetweenHitsMS', 0) > 0
                )
                if damage > 0:
                    add(prefix + 'damage', damage, node, ['Damage'])
                    if q.get('MinDamage', 0) > 0:
                        add(prefix + 'min_damage', q['MinDamage'], node, ['MinDamage'])
                elif damage < 0 and not sustained_healing:
                    add(
                        prefix + 'healing_per_hit',
                        -damage,
                        node,
                        ['Damage'],
                        'negative spell damage becomes positive healing',
                    )
                if (damage != 0 or q.get('AuraSpell') or role == 'aura' or q.get('BuildingDamagePermil')) and q.get(
                    'Radius', 0
                ) > 0:
                    add(prefix + ('aura_radius' if role == 'aura' else 'damage_radius'), q['Radius'], node, ['Radius'])
                for field, key, converter in [
                    ('ShieldProtectionPercent', 'damage_reduction_percent', None),
                    ('DamageBoostPercent', 'damage_increase_percent', None),
                    ('ExtraHealthPermil', 'health_increase_percent', lambda n: n / 10),
                    ('ExtraHealthMax', 'max_health_increase', None),
                    ('HeroDamageMultiplier', 'hero_damage_percent', None),
                    ('SpeedBoost', 'movement_speed_increase', None),
                    ('StunTimeMS', 'stun_duration', seconds),
                    ('FreezeTimeMS', 'freeze_duration', seconds),
                ]:
                    if q.get(field, 0) > 0 and (not (field == 'FreezeTimeMS' and q[field] < 1000)):
                        add(
                            prefix + key,
                            converter(q[field]) if converter else q[field],
                            node,
                            [field],
                            'milliseconds / 1000'
                            if converter is seconds
                            else 'per mille / 10'
                            if converter
                            else 'copy',
                        )
                if q.get('DestroyWalls'):
                    add(prefix + 'destroys_walls', True, node, ['DestroyWalls'])
                if q.get('DoesNotAffectOwner'):
                    add(prefix + 'affects_owner', False, node, ['DoesNotAffectOwner'], 'invert DoesNotAffectOwner')
                if q.get('ConeAngle', 0) > 0:
                    add(prefix + 'cone_angle', q['ConeAngle'], node, ['ConeAngle'])
                if q.get('BuildingDamagePermil', 0) > 0:
                    add(
                        prefix + 'building_damage_percent_per_hit',
                        q['BuildingDamagePermil'] / 10,
                        node,
                        ['BuildingDamagePermil'],
                        'per mille / 10',
                    )
                if q.get('TroopDamagePermil', 0) > 0:
                    add(
                        prefix + 'troop_damage_percent_per_hit',
                        q['TroopDamagePermil'] / 10,
                        node,
                        ['TroopDamagePermil'],
                        'per mille / 10',
                    )
                if q.get('ExecuteHealthPermil', 0) < 0 and not sustained_healing:
                    add(
                        prefix + 'health_healing_percent_per_hit',
                        -q['ExecuteHealthPermil'] / 10,
                        node,
                        ['ExecuteHealthPermil'],
                        'negative healing per mille / 10',
                    )
                if (damage != 0 or q.get('BuildingDamagePermil')) and q.get('NumberOfHits', 0) > 1:
                    if role != 'aura':
                        add(prefix + 'hits', q['NumberOfHits'], node, ['NumberOfHits'])
                    if q.get('TimeBetweenHitsMS', 0) > 0:
                        if not sustained_healing:
                            add(
                                prefix + 'hit_interval',
                                seconds(q['TimeBetweenHitsMS']),
                                node,
                                ['TimeBetweenHitsMS'],
                                'milliseconds / 1000',
                            )
                        if role == 'aura':
                            key = 'healing_per_second' if damage < 0 else 'dps'
                            if damage:
                                add(
                                    prefix + key,
                                    abs(damage) * 1000 / q['TimeBetweenHitsMS'],
                                    node,
                                    ['Damage', 'TimeBetweenHitsMS'],
                                    'absolute Damage * 1000 / TimeBetweenHitsMS',
                                )
            elif is_character and role == 'spawned':
                if node.get('status') != 'requires_runtime_level':
                    for field, key in [('Hitpoints', 'hitpoints'), ('DPS', 'dps')]:
                        if q.get(field, 0) > 0:
                            if prefix + key not in values:
                                add(prefix + key, q[field], node, [field])
                    if q.get('DPS', 0) < 0:
                        add(
                            prefix + 'healing_per_second',
                            -q['DPS'],
                            node,
                            ['DPS'],
                            'negative troop DPS becomes positive healing per second',
                        )
            count = q.get('TroopCount', q.get('UnitsToSpawn', 0))
            troop_name = q.get('SpawnedTroop', q.get('SummonTroop'))
            if (is_ability or is_spell) and count > 0 and troop_name:
                child = next((n for n in node.get('references', []) if n.get('source_name') == troop_name), {})
                troop = self.unit_identity(child, troop_name)
                level = q.get('TroopLevel', q.get('SpawnUpgradeLevel'))
                if q.get('CopySpawnnedTroopLevelFromAvatar'):
                    troop['level_from'] = 'hero'
                elif level is not None:
                    troop['level'] = level
                add(prefix + 'units_spawned', count, node, ['TroopCount' if 'TroopCount' in q else 'UnitsToSpawn'])
                add(prefix + 'spawned_unit', troop, node, ['SpawnedTroop' if 'SpawnedTroop' in q else 'SummonTroop'])
            for nested in node.get('abilities', []):
                visit(nested, prefix, 'spawned_ability')
            for child in node.get('references', []):
                via = child.get('via')
                if is_character:
                    continue
                if via in ('SpawnedTroop', 'SummonTroop'):
                    visit(child, prefix + 'spawned_unit_', 'spawned')
                elif via == 'GivenAbility':
                    visit(child, prefix + 'spawned_unit_', 'spawned_ability')
                elif via == 'AuraSpell':
                    visit(child, prefix, 'aura')
                else:
                    visit(child, prefix, 'effect')

        visit(root)
        if not values:
            if set(p) - ROOT_CONTROL and all((GRAPHICS.search(k) for k in set(p) - ROOT_CONTROL)):
                self.skipped.append({'key': root['source_name'], 'reason': 'visual_only_record'})
                return None
            raise ValueError(f"No player-facing effect for {root['source_name']}")
        if p.get('ChainShootingDistance') and p.get('DPS') and p.get('AttackSpeed'):
            add('damage', p['DPS'] * p['AttackSpeed'] / 1000, root, ['DPS', 'AttackSpeed'], 'DPS * AttackSpeed / 1000')
        name = root.get('name') or root['source_name']
        name_origin = {
            'source_name': root['source_name'],
            'TID': root.get('TID', {}).get('name'),
            'source': 'localization/texts.csv EN' if root.get('name') else 'source Name fallback',
        }
        if p.get('Regeneration', 0) > 0:
            name = self.texts['TID_REGENERATION_PER_SECOND']['EN'].split(':')[0]
            name_origin = {
                'TID': 'TID_REGENERATION_PER_SECOND',
                'source': 'shared regeneration label; source ability TID is reused for Ram Rider Charge',
            }
        metadata = {'key': root['source_name'], 'name': name}
        tids = {k: v for k, v in {'name': name_origin.get('TID'), 'info': root.get('TID', {}).get('info')}.items() if v}
        if tids:
            metadata['TID'] = tids
        if self.trigger(p):
            metadata['trigger'] = self.trigger(p)
        if root.get('info') and (not re.search('<(?:num|number)', root['info'])):
            metadata['info'] = root['info']
        self.trace.append(
            {
                'ability': root['source_name'],
                'source_level': root['level'],
                'name_origin': name_origin,
                'fields': origins,
            }
        )
        return (metadata, values)

    def intrinsic(self, row, owner):
        p = row['item_properties']
        nodes = row['item_references']
        groups = []
        if p.get('SummonTroopCount', 0) > 0:
            child = next((n for n in nodes if n.get('source_name') == p['SummonTroop']), {})
            unit = self.unit_identity(child, p['SummonTroop'])
            if p.get('SummonLevel') is not None:
                unit['level'] = p['SummonLevel']
            values = {'units_spawned': p['SummonTroopCount'], 'spawned_unit': unit}
            if p.get('SummonCooldown', 0) > 0:
                values['summon_interval'] = seconds(p['SummonCooldown'])
            groups.append(
                (
                    {
                        'key': 'intrinsic:summoning',
                        'name': 'Regular summoning',
                        'trigger': 'on_attack' if p.get('SpawnOnAttack') else 'ongoing',
                    },
                    values,
                )
            )
        if p.get('DieDamage', 0) > 0:
            groups.append(
                (
                    {'key': 'intrinsic:death_damage', 'name': 'Death damage', 'trigger': 'on_death'},
                    {'damage': p['DieDamage'], 'damage_radius': p['DieDamageRadius']},
                )
            )
        if any((a['properties'].get('DeactivateAfterPlayerInput') for a in row['abilities'])):
            values = {snake(k): p[k] for k in ('DPS', 'AttackRange', 'AttackSpeed', 'Speed') if k in p}
            values['movement_speed'] = values.pop('speed', 0)
            for source, target in [
                ('DPSLv2', 'dps_stage_2'),
                ('DPSLv3', 'dps_stage_3'),
                ('Lv2SwitchTime', 'stage_2_after'),
                ('Lv3SwitchTime', 'stage_3_after'),
            ]:
                if source in p:
                    values[target] = seconds(p[source]) if 'SwitchTime' in source else p[source]
            groups.append(
                ({'key': 'intrinsic:default_mode', 'name': 'Default mode', 'trigger': 'default_mode'}, values)
            )
        if p.get('SecondaryTroop') and p.get('SecondaryTroopCnt', 0) > 0:
            child = next((n for n in nodes if n.get('source_name') == p['SecondaryTroop']), {})
            unit = self.unit_identity(child, p['SecondaryTroop'])
            if child.get('status') == 'requires_runtime_level':
                unit['level_from'] = 'runtime'
            groups.append(
                (
                    {'key': 'intrinsic:secondary_troop', 'name': 'After landing', 'trigger': 'after_attack'},
                    {'units_spawned': p['SecondaryTroopCnt'], 'spawned_unit': unit},
                )
            )
        heal = p.get('HealOnActivation', 0)
        if heal > 0 and (not any((a['properties'].get('HealOnActivation') == heal for a in row['abilities']))):
            groups.append(
                (
                    {'key': 'intrinsic:heal_on_activation', 'name': 'Health recovery', 'trigger': 'activated'},
                    {'healing': heal},
                )
            )
        if p.get('AttackSpeedPercentage', 0) > 0:
            groups.append(
                (
                    {'key': 'intrinsic:attack_speed_bonus', 'name': 'Attack speed bonus'},
                    {'attack_speed_increase_percent': p['AttackSpeedPercentage']},
                )
            )
        for meta, values in groups:
            self.trace.append(
                {
                    'ability': meta['key'],
                    'owner_id': owner['item_id'],
                    'source_level': row['level'],
                    'name_origin': {'source': 'generated shared mechanic label'},
                    'source_file': owner['source_file'],
                    'source_name': owner['source_name'],
                    'owner_fields': list(p),
                    'exported_fields': list(values),
                }
            )
        return groups

    def build(self, include_source_keys=False):
        result = []
        for item in self.source:
            groups = {}
            for row in item['levels']:
                selected = [r for r in (self.project(n, item['item_type']) for n in row['abilities']) if r]
                selected.extend(self.intrinsic(row, item))
                for meta, values in selected:
                    group = groups.setdefault(meta['key'], {**meta, 'levels': []})
                    overrides = {
                        k: meta.get(k)
                        for k in set(meta) | set(group) - {'levels'}
                        if k != 'key' and group.get(k) != meta.get(k)
                    }
                    group['levels'].append({'level': row['level'], **values, **overrides})
            if not groups:
                raise ValueError(f"No exported abilities for {item['name']}")
            abilities = list(groups.values())
            if not include_source_keys:
                abilities = [{k: v for k, v in ability.items() if k != 'key'} for ability in abilities]
            result.append({'_id': item['item_id'], 'name': item['name'], 'abilities': abilities})
        return {'items': result}


def generate_abilities(tables, texts, catalog):
    """Return the standalone {items: [...]} collection; never mutate the catalog."""
    return AbilityGenerator(tables, texts, catalog).build()


def ability_tids(data):
    """Yield metadata TIDs, including overrides introduced at later levels."""
    for item in data['items']:
        for ability in item['abilities']:
            for metadata in (ability, *ability['levels']):
                yield from metadata.get('TID', {}).values()


def write_abilities(path, data):
    """Write only the individual ability collection."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
