"""Index locally available OGG clips through the game's entity and effect tables."""

import argparse
import csv
import io
import json
import lzma
import re
from collections import defaultdict
from pathlib import Path

import zstandard

from asset_sounds import SOUND_INDEX, eligible_sound_owners, load_ignored_ids, load_sound_index

WIKI = Path(__file__).resolve().parents[1]
TABLES = {
    'buildings': ('buildings', 'capital_buildings'),
    'traps': ('traps',),
    'troops': ('characters', 'capital_characters'),
    'heroes': ('heroes',),
    'equipment': ('character_items',),
    'pets': ('pets',),
    'spells': ('spells', 'capital_spells'),
    'guardians': ('guardians',),
    'skins': ('skins',),
    'helpers': ('villager_apprentices',),
    'decorations': ('decos',),
    'obstacles': ('obstacles', 'capital_obstacles'),
    'sceneries': ('village_backgrounds',),
}
REFERENCE_TABLES = (
    'effects', 'projectiles', 'capital_projectiles', 'special_abilities', 'weapons', 'scenery_clickables',
)
TABLE_FILES = {
    f'logic/{name}.csv': name
    for name in {name for group in TABLES.values() for name in group}.union(REFERENCE_TABLES)
}
TABLE_FILES['csv/animations.csv'] = 'animations'

# These files are named for their owners but have no link in the game tables.
LOOSE_SOUND_PREFIXES = {
    'helpers:93000000': ('ApprenticeBuilder_',),
    'helpers:93000001': ('Researcher_',),
    'helpers:93000002': ('Alchemist_',),
    'helpers:93000003': ('Prospector_',),
    'equipment:90000044': ('Ability_ChainPants_',),
    'guardians:107000000': ('RangedGuardian_',),
    'guardians:107000001': ('MeleeGuardian_',),
    'pets:73000016': ('Pet_Sneezy_Spirit_',),
    'pets:73000017': ('pet_greedyraven_',),
    'spells:26000053': ('recall_spell_back_',),
    'spells:26000070': ('overgrowth_spell_retract_',),
    'spells:26000109': ('Spell_de_IceBlock_',),
    'troops:4000142': ('Troop_BarrelSnake_', 'Troop_Snake_Bite.'),
    'troops:4000150': ('Troop_Furnace_FireSpirit_',),
    'troops:4000167': ('Troop_MeteorGolem_',),
    'troops:4000177': ('Troop_MeteorGolem_',),
    'troops:4000185': ('sfx_tro_erider_',),
}

HERO_VOICES = {
    'heroes:28000000': 'BarbarianKing',
    'heroes:28000001': 'ArcherQueen',
    'heroes:28000002': 'GrandWarden',
    'heroes:28000004': 'RoyalChampion',
    'heroes:28000006': 'MinionPrince',
    'heroes:28000007': 'DragonDuke',
}


def csv_text(data):
    if data.startswith(b'Sig:'):
        data = data[68:]
    if data.startswith(bytes.fromhex('28b52ffd')):
        data = zstandard.decompress(data)
    elif data.startswith(b']'):
        data = lzma.LZMADecompressor().decompress(data[:9] + bytes(4) + data[9:])
    return data.decode('utf-8-sig')


def read_table(data):
    reader = csv.DictReader(io.StringIO(csv_text(data)))
    next(reader, None)  # The first row after the header declares field types.
    rows = defaultdict(list)
    name = ''
    for row in reader:
        if row['Name']:
            name = row['Name']
        if name:
            rows[name].append(row)
    return rows


def read_animations(data):
    groups = defaultdict(list)
    rows = iter(csv.reader(io.StringIO(csv_text(data))))
    columns = []
    name = ''
    for row in rows:
        if row and row[0].strip():
            name = row[0].strip()
            # Each animation group has its own field definitions and type row.
            columns = next(rows, [])
            next(rows, None)
        elif name and row:
            groups[name].append(dict(zip(columns, row)))
    return groups


def read_sound_table(file_path, data):
    return read_animations(data) if file_path == 'csv/animations.csv' else read_table(data)


def load_tables(sources):
    tables = defaultdict(dict)
    for root in sources:
        for relative_path, name in TABLE_FILES.items():
            path = root / relative_path
            if path.is_file():
                tables[name].update(read_sound_table(relative_path, path.read_bytes()))
    return tables


def fields(rows, predicate):
    for row in rows:
        for field, value in row.items():
            if field and value and predicate(field):
                yield from (part.strip() for part in value.split(';') if part.strip())


def clip_family(path):
    stem = Path(path).stem.casefold()
    stem = re.sub(r'(?:_dl_opt|_lp|_loop)$', '', stem)
    return re.sub(r'_[0-9]{2}$', '', stem)


def sound_index(tables, clip_paths, data, previous, ignored):
    tables = defaultdict(dict, tables)
    families = defaultdict(list)
    for path in sorted(clip_paths):
        families[clip_family(path)].append(path)
    relations = [
        (tables['effects'], lambda key: 'Effect' in key and not key.startswith('Inherit')),
        (tables['projectiles'] | tables['capital_projectiles'], lambda key: 'Projectile' in key),
        (tables['spells'] | tables['capital_spells'], lambda key: 'Spell' in key),
        (tables['special_abilities'], lambda key: 'Abilities' in key or key in ('SpecialAbility', 'Ability')),
        (tables['weapons'], lambda key: 'Weapon' in key),
        (tables['characters'] | tables['capital_characters'], lambda key: key in (
            'CharacterDatas', 'SummonTroop', 'SecondaryTroop', 'BunkerTroops', 'DefensiveTroop',
        )),
        (tables['skins'], lambda key: key in ('DefaultSkin', 'DisplaySkin', 'Skin', 'MenuSkin')),
        (tables['animations'], lambda key: key in (
            'Animation', 'AltAnimation', 'AnimationName', 'SpawnedTroopAnimation',
        )),
        (tables['scenery_clickables'], lambda key: key == 'Clickables'),
    ]

    def collect(rows):
        queue = [rows]
        sounds = set()
        visited = set()
        while queue:
            batch = queue.pop()
            for value in fields(batch, lambda key: True):
                if value.startswith('sfx/') and value.lower().endswith('.ogg'):
                    sounds.update(families.get(clip_family(value), ()))
            for table, field_matches in relations:
                for name in fields(batch, field_matches):
                    key = (id(table), name)
                    if key not in visited and name in table:
                        visited.add(key)
                        queue.append(table[name])
        return sounds

    # Current fingerprints and local archives can omit older tables or clips.
    # Retain verified associations for clips that still exist in either input.
    owners = eligible_sound_owners(data, ignored)
    mapping = defaultdict(set, {
        key: set(paths).intersection(clip_paths)
        for key, paths in previous.items()
        if key in owners
    })
    for kind, names in TABLES.items():
        by_id = {}
        by_tid = defaultdict(dict)
        for table in names:
            for name, rows in tables[table].items():
                initial = rows[0]
                if initial.get('GlobalID'):
                    by_id[initial['GlobalID']] = rows
                if initial.get('TID'):
                    by_tid[initial['TID']][name] = rows
        for entry in data.get(kind, []):
            key = f"{kind}:{entry['_id']}"
            if key not in owners:
                continue
            tid = entry.get('TID', {}).get('name')
            candidates = by_tid.get(tid, {})
            fallback = next(iter(candidates.values())) if len(candidates) == 1 else candidates.get(entry['name'], ())
            rows = by_id.get(str(entry['_id']), fallback)
            paths = collect(rows)
            for prefix in LOOSE_SOUND_PREFIXES.get(key, ()):
                paths.update(path for path in clip_paths if Path(path).name.casefold().startswith(prefix.casefold()))
            if speaker := HERO_VOICES.get(key):
                voice = re.compile(rf'sfx/vo_{speaker}_(?:angry|happy|neutral|sad|scared)(?:_[0-9]+)?\.ogg', re.I)
                paths.update(path for path in clip_paths if voice.fullmatch(path))
            mapping[key].update(paths)
    return {key: sorted(paths) for key, paths in mapping.items() if paths}


def write_index(mapping):
    rendered = json.dumps(mapping, indent=2, sort_keys=True) + '\n'
    if not SOUND_INDEX.exists() or SOUND_INDEX.read_text(encoding='utf-8') != rendered:
        SOUND_INDEX.write_text(rendered, encoding='utf-8')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('sources', nargs='+', type=Path, help='Extracted assets directories, oldest first')
    args = parser.parse_args()
    clips = {f'sfx/{path.name}' for path in (WIKI.parent / 'assets/sfx').glob('*.ogg') if path.is_file()}
    if not clips:
        parser.error('assets/sfx contains no OGG clips')
    data = json.loads((WIKI.parent / 'assets/static_data.json').read_text(encoding='utf-8'))
    mapping = sound_index(load_tables(args.sources), clips, data, load_sound_index(), load_ignored_ids())
    write_index(mapping)
    print(f'Indexed {sum(map(len, mapping.values()))} clip references across {len(mapping)} wiki entries')


if __name__ == '__main__':
    main()
